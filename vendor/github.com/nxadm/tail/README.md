<<<<<<< HEAD
![ci](https://github.com/nxadm/tail/workflows/ci/badge.svg)[![Go Reference](https://pkg.go.dev/badge/github.com/nxadm/tail.svg)](https://pkg.go.dev/github.com/nxadm/tail)

# tail functionality in Go

nxadm/tail provides a Go library that emulates the features of the BSD `tail`
program. The library comes with full support for truncation/move detection as
it is designed to work with log rotation tools. The library works on all
operating systems supported by Go, including POSIX systems like Linux and
*BSD, and MS Windows. Go 1.9 is the oldest compiler release supported.

A simple example:

```Go
// Create a tail
t, err := tail.TailFile(
	"/var/log/nginx.log", tail.Config{Follow: true, ReOpen: true})
=======
[![Build Status](https://travis-ci.org/nxadm/tail.svg?branch=master)](https://travis-ci.org/nxadm/tail)

This is repo is forked from the dormant upstream repo at
[hpcloud](https://github.com/hpcloud/tail). This fork adds support for go
modules, updates the dependencies, adds features and fixes bugs. Go 1.9 is
the oldest compiler release supported.

# Go package for tail-ing files

A Go package striving to emulate the features of the BSD `tail` program.

```Go
t, err := tail.TailFile("/var/log/nginx.log", tail.Config{Follow: true})
>>>>>>> 33cbc1d (add batchrelease controller)
if err != nil {
    panic(err)
}

<<<<<<< HEAD
// Print the text of each received line
=======
>>>>>>> 33cbc1d (add batchrelease controller)
for line := range t.Lines {
    fmt.Println(line.Text)
}
```

<<<<<<< HEAD
See [API documentation](https://pkg.go.dev/github.com/nxadm/tail).
=======
See [API documentation](http://godoc.org/github.com/nxadm/tail).

## Log rotation

Tail comes with full support for truncation/move detection as it is
designed to work with log rotation tools.
>>>>>>> 33cbc1d (add batchrelease controller)

## Installing

    go get github.com/nxadm/tail/...

<<<<<<< HEAD
## History

This project is an active, drop-in replacement for the
[abandoned](https://en.wikipedia.org/wiki/HPE_Helion) Go tail library at
[hpcloud](https://github.com/hpcloud/tail). Next to
[addressing open issues/PRs of the original project](https://github.com/nxadm/tail/issues/6),
nxadm/tail continues the development by keeping up to date with the Go toolchain
(e.g. go modules) and dependencies, completing the documentation, adding features
and fixing bugs.

## Examples
Examples, e.g. used to debug an issue, are kept in the [examples directory](/examples).
=======
## Windows support

This package [needs assistance](https://github.com/nxadm/tail/labels/Windows) for full Windows support.
>>>>>>> 33cbc1d (add batchrelease controller)
