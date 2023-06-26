/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package builder

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strconv"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// collectSharedParameters finds those parameters that show up often and hence can be
// shared across all the paths to save space.
func collectSharedParameters(sp *spec.Swagger) (namesByJSON map[string]string, ret map[string]spec.Parameter, err error) {

	if sp == nil || sp.Paths == nil {
		return nil, nil, nil
	}

	countsByJSON := map[string]int{}
	shared := map[string]spec.Parameter{}
	var keys []string

	collect := func(p *spec.Parameter) error {
		bs, err := json.Marshal(p)
		if err != nil {
			return err
		}

		countsByJSON[string(bs)]++
		if count := countsByJSON[string(bs)]; count == 1 {
			shared[string(bs)] = *p
			keys = append(keys, string(bs))
		}

		return nil
	}

	for _, path := range sp.Paths.Paths {
		// per operation parameters
		for _, op := range []*spec.Operation{path.Get, path.Put, path.Post, path.Delete, path.Options, path.Head, path.Patch} {
			if op == nil {
				continue // shouldn't happen, but ignore if it does
			}
			for _, p := range op.Parameters {
				if p.Ref.String() != "" {
					// shouldn't happen, but ignore if it does
					continue
				}
				if err := collect(&p); err != nil {
					return nil, nil, err
				}
			}
		}

		// per path parameters
		for _, p := range path.Parameters {
			if p.Ref.String() != "" {
				continue // shouldn't happen, but ignore if it does
			}
			if err := collect(&p); err != nil {
				return nil, nil, err
			}
		}
	}

	// name deterministically
	sort.Strings(keys)
	ret = map[string]spec.Parameter{}
	namesByJSON = map[string]string{}
	for _, k := range keys {
		name := shared[k].Name
		if name == "" {
			name = "param"
		}
		name += "-" + base64Hash(k)
		i := 0
		for {
			if _, ok := ret[name]; !ok {
				ret[name] = shared[k]
				namesByJSON[k] = name
				break
			}
			i++ // only on hash conflict, unlikely with our few variants
			name = shared[k].Name + "-" + strconv.Itoa(i)
		}
	}

	return namesByJSON, ret, nil
}

func base64Hash(s string) string {
	hash := sha256.Sum224([]byte(s))
	return base64.URLEncoding.EncodeToString(hash[:6]) // 8 characters
}

func replaceSharedParameters(sharedParameterNamesByJSON map[string]string, sp *spec.Swagger) (*spec.Swagger, error) {
	if sp == nil || sp.Paths == nil {
		return sp, nil
	}

	ret := sp

	firstPathChange := true
	for k, path := range sp.Paths.Paths {
		pathChanged := false

		// per operation parameters
		for _, op := range []**spec.Operation{&path.Get, &path.Put, &path.Post, &path.Delete, &path.Options, &path.Head, &path.Patch} {
			if *op == nil {
				continue
			}

			firstParamChange := true
			for i := range (*op).Parameters {
				p := (*op).Parameters[i]

				if p.Ref.String() != "" {
					// shouldn't happen, but be idem-potent if it does
					continue
				}

				bs, err := json.Marshal(p)
				if err != nil {
					return nil, err
				}

				if name, ok := sharedParameterNamesByJSON[string(bs)]; ok {
					if firstParamChange {
						orig := *op
						*op = &spec.Operation{}
						**op = *orig
						(*op).Parameters = make([]spec.Parameter, len(orig.Parameters))
						copy((*op).Parameters, orig.Parameters)
						firstParamChange = false
					}

					(*op).Parameters[i] = spec.Parameter{
						Refable: spec.Refable{
							Ref: spec.MustCreateRef("#/parameters/" + name),
						},
					}
					pathChanged = true
				}
			}
		}

		// per path parameters
		firstParamChange := true
		for i := range path.Parameters {
			p := path.Parameters[i]

			if p.Ref.String() != "" {
				// shouldn't happen, but be idem-potent if it does
				continue
			}

			bs, err := json.Marshal(p)
			if err != nil {
				return nil, err
			}

			if name, ok := sharedParameterNamesByJSON[string(bs)]; ok {
				if firstParamChange {
					orig := path.Parameters
					path.Parameters = make([]spec.Parameter, len(orig))
					copy(path.Parameters, orig)
					firstParamChange = false
				}

				path.Parameters[i] = spec.Parameter{
					Refable: spec.Refable{
						Ref: spec.MustCreateRef("#/parameters/" + name),
					},
				}
				pathChanged = true
			}
		}

		if pathChanged {
			if firstPathChange {
				clone := *sp
				ret = &clone

				pathsClone := *ret.Paths
				ret.Paths = &pathsClone

				ret.Paths.Paths = make(map[string]spec.PathItem, len(sp.Paths.Paths))
				for k, v := range sp.Paths.Paths {
					ret.Paths.Paths[k] = v
				}

				firstPathChange = false
			}
			ret.Paths.Paths[k] = path
		}
	}

	return ret, nil
}
