module github.com/onsi/ginkgo

<<<<<<< HEAD
go 1.16

require (
	github.com/go-task/slim-sprig v0.0.0-20210107165309-348f09dbbbc0
	github.com/nxadm/tail v1.4.8
	github.com/onsi/gomega v1.10.1
	golang.org/x/sys v0.0.0-20210112080510-489259a85091
	golang.org/x/tools v0.0.0-20201224043029-2b0845dc783e
)

retract v1.16.3 // git tag accidentally associated with incorrect git commit
=======
require (
	github.com/fsnotify/fsnotify v1.4.9 // indirect
	github.com/nxadm/tail v1.4.4
	github.com/onsi/gomega v1.10.1
	golang.org/x/sys v0.0.0-20200519105757-fe76b779f299
	golang.org/x/text v0.3.2 // indirect
)

go 1.13
>>>>>>> 33cbc1d (add batchrelease controller)
