package testingtproxy

import (
	"fmt"
	"io"
)

type failFunc func(message string, callerSkip ...int)
<<<<<<< HEAD
type skipFunc func(message string, callerSkip ...int)
type failedFunc func() bool
type nameFunc func() string

func New(writer io.Writer, fail failFunc, skip skipFunc, failed failedFunc, name nameFunc, offset int) *ginkgoTestingTProxy {
=======

func New(writer io.Writer, fail failFunc, offset int) *ginkgoTestingTProxy {
>>>>>>> 33cbc1d (add batchrelease controller)
	return &ginkgoTestingTProxy{
		fail:   fail,
		offset: offset,
		writer: writer,
<<<<<<< HEAD
		skip:   skip,
		failed: failed,
		name:   name,
=======
>>>>>>> 33cbc1d (add batchrelease controller)
	}
}

type ginkgoTestingTProxy struct {
	fail   failFunc
<<<<<<< HEAD
	skip   skipFunc
	failed failedFunc
	name   nameFunc
=======
>>>>>>> 33cbc1d (add batchrelease controller)
	offset int
	writer io.Writer
}

<<<<<<< HEAD
func (t *ginkgoTestingTProxy) Cleanup(func()) {
	// No-op
}

func (t *ginkgoTestingTProxy) Setenv(kev, value string) {
	fmt.Println("Setenv is a noop for Ginkgo at the moment but will be implemented in V2")
	// No-op until Cleanup is implemented
}

=======
>>>>>>> 33cbc1d (add batchrelease controller)
func (t *ginkgoTestingTProxy) Error(args ...interface{}) {
	t.fail(fmt.Sprintln(args...), t.offset)
}

func (t *ginkgoTestingTProxy) Errorf(format string, args ...interface{}) {
	t.fail(fmt.Sprintf(format, args...), t.offset)
}

func (t *ginkgoTestingTProxy) Fail() {
	t.fail("failed", t.offset)
}

func (t *ginkgoTestingTProxy) FailNow() {
	t.fail("failed", t.offset)
}

<<<<<<< HEAD
func (t *ginkgoTestingTProxy) Failed() bool {
	return t.failed()
}

=======
>>>>>>> 33cbc1d (add batchrelease controller)
func (t *ginkgoTestingTProxy) Fatal(args ...interface{}) {
	t.fail(fmt.Sprintln(args...), t.offset)
}

func (t *ginkgoTestingTProxy) Fatalf(format string, args ...interface{}) {
	t.fail(fmt.Sprintf(format, args...), t.offset)
}

<<<<<<< HEAD
func (t *ginkgoTestingTProxy) Helper() {
	// No-op
}

=======
>>>>>>> 33cbc1d (add batchrelease controller)
func (t *ginkgoTestingTProxy) Log(args ...interface{}) {
	fmt.Fprintln(t.writer, args...)
}

func (t *ginkgoTestingTProxy) Logf(format string, args ...interface{}) {
	t.Log(fmt.Sprintf(format, args...))
}

<<<<<<< HEAD
func (t *ginkgoTestingTProxy) Name() string {
	return t.name()
}

func (t *ginkgoTestingTProxy) Parallel() {
	// No-op
}

func (t *ginkgoTestingTProxy) Skip(args ...interface{}) {
	t.skip(fmt.Sprintln(args...), t.offset)
}

func (t *ginkgoTestingTProxy) SkipNow() {
	t.skip("skip", t.offset)
}

func (t *ginkgoTestingTProxy) Skipf(format string, args ...interface{}) {
	t.skip(fmt.Sprintf(format, args...), t.offset)
=======
func (t *ginkgoTestingTProxy) Failed() bool {
	return false
}

func (t *ginkgoTestingTProxy) Parallel() {
}

func (t *ginkgoTestingTProxy) Skip(args ...interface{}) {
	fmt.Println(args...)
}

func (t *ginkgoTestingTProxy) Skipf(format string, args ...interface{}) {
	t.Skip(fmt.Sprintf(format, args...))
}

func (t *ginkgoTestingTProxy) SkipNow() {
>>>>>>> 33cbc1d (add batchrelease controller)
}

func (t *ginkgoTestingTProxy) Skipped() bool {
	return false
}
<<<<<<< HEAD

func (t *ginkgoTestingTProxy) TempDir() string {
	// No-op
	return ""
}
=======
>>>>>>> 33cbc1d (add batchrelease controller)
