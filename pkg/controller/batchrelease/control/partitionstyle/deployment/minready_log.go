/*
Copyright 2026 The Kruise Authors.

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

package deployment

import (
	"fmt"
	"strings"

	"k8s.io/klog/v2"
)

// warningS logs at warning severity with the same call shape as klog.InfoS/ErrorS.
// klog v2.120.1 does not expose WarningS, so MinReady uses this helper locally.
func warningS(err error, msg string, keysAndValues ...interface{}) {
	klog.Warning(formatStructuredLog(err, msg, keysAndValues...))
}

func formatStructuredLog(err error, msg string, keysAndValues ...interface{}) string {
	var b strings.Builder
	b.WriteString(msg)
	if err != nil {
		fmt.Fprintf(&b, " err=%v", err)
	}
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		fmt.Fprintf(&b, " %v=%v", keysAndValues[i], keysAndValues[i+1])
	}
	return b.String()
}
