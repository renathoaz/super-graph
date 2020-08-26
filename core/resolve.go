package core

import (
	"fmt"
	"hash/maphash"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/renathoaz/super-graph/core/internal/sdata"
	"github.com/renathoaz/super-graph/jsn"
)

type resolvFn struct {
	IDField []byte
	Path    [][]byte
	Fn      func(h http.Header, id []byte) ([]byte, error)
}

func (sg *SuperGraph) initResolvers() error {
	var err error
	sg.rmap = make(map[uint64]resolvFn)

	for _, t := range sg.conf.Tables {
		if err = sg.initRemotes(t); err != nil {
			return fmt.Errorf("resolvers: %w", err)
		}
	}

	return nil
}

func (sg *SuperGraph) initRemotes(t Table) error {
	h := maphash.Hash{}
	h.SetSeed(sg.hashSeed)

	for _, r := range t.Remotes {
		// Defines the table column to be used as an id in the
		// remote reques
		var col *sdata.DBColumn

		ti, err := sg.schema.GetTableInfo(t.Name)
		if err != nil {
			return err
		}

		// If no table column specified in the config then
		// use the primary key of the table as the id
		if r.ID != "" {
			idcol, err := ti.GetColumn(r.ID)
			if err != nil {
				return err
			}
			col = idcol
		} else {
			col = ti.PrimaryCol
		}

		idk := fmt.Sprintf("__%s_%s", t.Name, col.Name)

		// Register a relationship between the remote data
		// and the database table
		val := sdata.DBRel{Type: sdata.RelRemote}
		val.Left.Col = col
		val.Right.VTable = idk

		if err := sg.schema.SetRel(r.Name, t.Name, &val); err != nil {
			return err
		}

		// The function thats called to resolve this remote
		// data request
		fn := buildFn(r)

		path := [][]byte{}
		for _, p := range strings.Split(r.Path, ".") {
			path = append(path, []byte(p))
		}

		rf := resolvFn{
			IDField: []byte(idk),
			Path:    path,
			Fn:      fn,
		}

		// Index resolver obj by parent and child names
		sg.rmap[mkkey(&h, r.Name, t.Name)] = rf

		// Index resolver obj by IDField
		_, _ = h.Write(rf.IDField)
		sg.rmap[h.Sum64()] = rf
	}

	return nil
}

func buildFn(r Remote) func(http.Header, []byte) ([]byte, error) {
	reqURL := strings.Replace(r.URL, "$id", "%s", 1)
	client := &http.Client{}

	fn := func(hdr http.Header, id []byte) ([]byte, error) {
		uri := fmt.Sprintf(reqURL, id)
		req, err := http.NewRequest("GET", uri, nil)
		if err != nil {
			return nil, err
		}

		if host, ok := hdr["Host"]; ok {
			req.Host = host[0]
		}

		for _, v := range r.SetHeaders {
			req.Header.Set(v.Name, v.Value)
		}

		for _, v := range r.PassHeaders {
			req.Header.Set(v, hdr.Get(v))
		}

		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to '%s': %v", uri, err)
		}
		defer res.Body.Close()

		// if r.Debug {
		// reqDump, err := httputil.DumpRequestOut(req, true)
		// if err != nil {
		// 	return nil, err
		// }

		// resDump, err := httputil.DumpResponse(res, true)
		// if err != nil {
		// 	return nil, err
		// }

		// logger.Debug().Msgf("Remote Request Debug:\n%s\n%s",
		// 	reqDump, resDump)
		// }

		if res.StatusCode != 200 {
			return nil,
				fmt.Errorf("server responded with a %d", res.StatusCode)
		}

		b, err := ioutil.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}

		if err := jsn.ValidateBytes(b); err != nil {
			return nil, err
		}

		return b, nil
	}

	return fn
}
