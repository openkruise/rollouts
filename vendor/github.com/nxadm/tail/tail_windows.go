<<<<<<< HEAD
// Copyright (c) 2019 FOSS contributors of https://github.com/nxadm/tail
=======
>>>>>>> 33cbc1d (add batchrelease controller)
// +build windows

package tail

import (
<<<<<<< HEAD
	"os"

	"github.com/nxadm/tail/winfile"
)

// Deprecated: this function is only useful internally and, as such,
// it will be removed from the API in a future major release.
//
// OpenFile proxies a os.Open call for a file so it can be correctly tailed
// on POSIX and non-POSIX OSes like MS Windows.
=======
	"github.com/nxadm/tail/winfile"
	"os"
)

>>>>>>> 33cbc1d (add batchrelease controller)
func OpenFile(name string) (file *os.File, err error) {
	return winfile.OpenFile(name, os.O_RDONLY, 0)
}
