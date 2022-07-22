/*
Copyright 2022.

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

package rollouthistory

import (
	"crypto/rand"
	"math/big"
	"strings"
)

var CHARS = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
	"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"}

func RandAllString(lenNum int) string {
	str := strings.Builder{}
	for i := 0; i < lenNum; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(36))
		if err != nil {
			return ""
		}
		l := CHARS[n.Int64()]
		str.WriteString(l)
	}
	return str.String()
}
