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

package writer

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/openkruise/rollouts/pkg/webhook/util/generator"
)

// TestWriteCertsToDirPermissions asserts the cert directory and the files it
// holds get restrictive permissions: the directory is owner-only (0700), the
// CA/server private keys are 0600, and the public certificates may stay 0644.
// This locks down the fix for the over-permissive 0777 dir / 0666 key writes.
func TestWriteCertsToDirPermissions(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "certs")

	certs := &generator.Artifacts{
		CAKey:  []byte("ca-key"),
		CACert: []byte("ca-cert"),
		Key:    []byte("server-key"),
		Cert:   []byte("server-cert"),
	}

	if err := WriteCertsToDir(dir, certs); err != nil {
		t.Fatalf("WriteCertsToDir failed: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Fatalf("cert dir perm = %#o, want 0700", perm)
	}

	privateKeys := []string{CAKeyName, ServerKeyName, ServerKeyName2}
	for _, name := range privateKeys {
		assertFilePerm(t, dir, name, 0600)
	}

	publicCerts := []string{CACertName, ServerCertName, ServerCertName2}
	for _, name := range publicCerts {
		assertFilePerm(t, dir, name, 0644)
	}
}

func assertFilePerm(t *testing.T, dir, name string, want os.FileMode) {
	t.Helper()
	// The atomic writer exposes the payload through a symlink to a timestamped
	// directory; Stat (not Lstat) follows it to the real file we care about.
	info, err := os.Stat(path.Join(dir, name))
	if err != nil {
		t.Fatalf("stat %s failed: %v", name, err)
	}
	if perm := info.Mode().Perm(); perm != want {
		t.Fatalf("%s perm = %#o, want %#o", name, perm, want)
	}
}
