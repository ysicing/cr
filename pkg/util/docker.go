/*
 * // Copyright (c) 2021. The 51talk EFF Team Authors.
 * //
 * // Licensed under the Apache License, Version 2.0 (the "License");
 * // you may not use this file except in compliance with the License.
 * // You may obtain a copy of the License at
 * //
 * //     http://www.apache.org/licenses/LICENSE-2.0
 * //
 * // Unless required by applicable law or agreed to in writing, software
 * // distributed under the License is distributed on an "AS IS" BASIS,
 * // See the License for the specific language governing permissions and
 * // limitations under the License.
 */

package util

import (
	"bytes"
	b64 "encoding/base64"
	"fmt"
	"text/template"
)

const dockerconfigjson = `
{
  "auths": {
    "{{.CRHost}}": {
      "username": "{{.CRUSER}}",
      "password": "{{.CRPASS}}",
      "auth": "{{.CRAUTH}}"
    }
  }
}
`

type ConfigJson struct {
	CRHost string
	CRUSER string
	CRPASS string
	CRAUTH string
}

func GenDockerConfigJSON(domain, user, pass string) string {
	var b bytes.Buffer
	j := ConfigJson{
		CRHost: domain,
		CRUSER: user,
		CRPASS: b64encode(pass),
		CRAUTH: b64encode(fmt.Sprintf("%v:%v", user, pass)),
	}
	t, _ := template.New("configjson").Parse(dockerconfigjson)
	if err := t.Execute(&b, j); err != nil {
		return ""
	}
	return b.String()
}

// b64encode base64加密
func b64encode(code string) string {
	return b64.StdEncoding.EncodeToString([]byte(code))
}
