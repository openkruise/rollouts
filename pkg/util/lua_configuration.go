/*
Copyright 2022 The Kruise Authors.

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

package util

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"k8s.io/klog/v2"
)

// patch -> file.Content,
// for example:  lua_configuration/trafficrouting_ingress/ingress.lua -> ingress.lua content
var luaConfigurationList map[string]string

func init() {
	luaConfigurationList = map[string]string{}
	_ = filepath.Walk("./lua_configuration", func(path string, f os.FileInfo, err error) error {
		if err != nil {
			klog.Warningf("filepath walk ./lua_configuration failed: %s", err.Error())
			return err
		}
		if f.IsDir() {
			return nil
		}
		var data []byte
		data, err = ioutil.ReadFile(filepath.Clean(path))
		if err != nil {
			klog.Errorf("Read file %s failed: %s", path, err.Error())
			return err
		}
		luaConfigurationList[path] = string(data)
		return nil
	})
	klog.Infof("Init Lua Configuration(%s)", DumpJSON(luaConfigurationList))
}

func GetLuaConfigurationContent(key string) string {
	return luaConfigurationList[key]
}
