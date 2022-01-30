/*
Gomega is the Ginkgo BDD-style testing framework's preferred matcher library.

The godoc documentation describes Gomega's API.  More comprehensive documentation (with examples!) is available at http://onsi.github.io/gomega/

Gomega on Github: http://github.com/onsi/gomega

Learn more about Ginkgo online: http://onsi.github.io/ginkgo

Ginkgo on Github: http://github.com/onsi/ginkgo

Gomega is MIT-Licensed
*/
package gomega

import (
<<<<<<< HEAD
	"errors"
	"fmt"
	"time"

	"github.com/onsi/gomega/internal"
	"github.com/onsi/gomega/types"
)

const GOMEGA_VERSION = "1.17.0"

const nilGomegaPanic = `You are trying to make an assertion, but haven't registered Gomega's fail handler.
=======
	"fmt"
	"reflect"
	"time"

	"github.com/onsi/gomega/internal/assertion"
	"github.com/onsi/gomega/internal/asyncassertion"
	"github.com/onsi/gomega/internal/testingtsupport"
	"github.com/onsi/gomega/types"
)

const GOMEGA_VERSION = "1.10.2"

const nilFailHandlerPanic = `You are trying to make an assertion, but Gomega's fail handler is nil.
>>>>>>> 33cbc1d (add batchrelease controller)
If you're using Ginkgo then you probably forgot to put your assertion in an It().
Alternatively, you may have forgotten to register a fail handler with RegisterFailHandler() or RegisterTestingT().
Depending on your vendoring solution you may be inadvertently importing gomega and subpackages (e.g. ghhtp, gexec,...) from different locations.
`

<<<<<<< HEAD
// Gomega describes the essential Gomega DSL. This interface allows libraries
// to abstract between the standard package-level function implementations
// and alternatives like *WithT.
//
// The types in the top-level DSL have gotten a bit messy due to earlier depracations that avoid stuttering
// and due to an accidental use of a concrete type (*WithT) in an earlier release.
//
// As of 1.15 both the WithT and Ginkgo variants of Gomega are implemented by the same underlying object
// however one (the Ginkgo variant) is exported as an interface (types.Gomega) whereas the other (the withT variant)
// is shared as a concrete type (*WithT, which is aliased to *internal.Gomega).  1.15 did not clean this mess up to ensure
// that declarations of *WithT in existing code are not broken by the upgrade to 1.15.
type Gomega = types.Gomega

// DefaultGomega supplies the standard package-level implementation
var Default = Gomega(internal.NewGomega(internal.FetchDefaultDurationBundle()))

// NewGomega returns an instance of Gomega wired into the passed-in fail handler.
// You generally don't need to use this when using Ginkgo - RegisterFailHandler will wire up the global gomega
// However creating a NewGomega with a custom fail handler can be useful in contexts where you want to use Gomega's
// rich ecosystem of matchers without causing a test to fail.  For example, to aggregate a series of potential failures
// or for use in a non-test setting.
func NewGomega(fail types.GomegaFailHandler) Gomega {
	return internal.NewGomega(Default.(*internal.Gomega).DurationBundle).ConfigureWithFailHandler(fail)
}

// WithT wraps a *testing.T and provides `Expect`, `Eventually`, and `Consistently` methods.  This allows you to leverage
// Gomega's rich ecosystem of matchers in standard `testing` test suites.
//
// Use `NewWithT` to instantiate a `WithT`
//
// As of 1.15 both the WithT and Ginkgo variants of Gomega are implemented by the same underlying object
// however one (the Ginkgo variant) is exported as an interface (types.Gomega) whereas the other (the withT variant)
// is shared as a concrete type (*WithT, which is aliased to *internal.Gomega).  1.15 did not clean this mess up to ensure
// that declarations of *WithT in existing code are not broken by the upgrade to 1.15.
type WithT = internal.Gomega

// GomegaWithT is deprecated in favor of gomega.WithT, which does not stutter.
type GomegaWithT = WithT

// NewWithT takes a *testing.T and returngs a `gomega.WithT` allowing you to use `Expect`, `Eventually`, and `Consistently` along with
// Gomega's rich ecosystem of matchers in standard `testing` test suits.
//
//    func TestFarmHasCow(t *testing.T) {
//        g := gomega.NewWithT(t)
//
//        f := farm.New([]string{"Cow", "Horse"})
//        g.Expect(f.HasCow()).To(BeTrue(), "Farm should have cow")
//     }
func NewWithT(t types.GomegaTestingT) *WithT {
	return internal.NewGomega(Default.(*internal.Gomega).DurationBundle).ConfigureWithT(t)
}

// NewGomegaWithT is deprecated in favor of gomega.NewWithT, which does not stutter.
var NewGomegaWithT = NewWithT

// RegisterFailHandler connects Ginkgo to Gomega. When a matcher fails
// the fail handler passed into RegisterFailHandler is called.
func RegisterFailHandler(fail types.GomegaFailHandler) {
	Default.(*internal.Gomega).ConfigureWithFailHandler(fail)
}

// RegisterFailHandlerWithT is deprecated and will be removed in a future release.
// users should use RegisterFailHandler, or RegisterTestingT
func RegisterFailHandlerWithT(_ types.GomegaTestingT, fail types.GomegaFailHandler) {
	fmt.Println("RegisterFailHandlerWithT is deprecated.  Please use RegisterFailHandler or RegisterTestingT instead.")
	Default.(*internal.Gomega).ConfigureWithFailHandler(fail)
}

// RegisterTestingT connects Gomega to Golang's XUnit style
// Testing.T tests.  It is now deprecated and you should use NewWithT() instead to get a fresh instance of Gomega for each test.
func RegisterTestingT(t types.GomegaTestingT) {
	Default.(*internal.Gomega).ConfigureWithT(t)
=======
var globalFailWrapper *types.GomegaFailWrapper

var defaultEventuallyTimeout = time.Second
var defaultEventuallyPollingInterval = 10 * time.Millisecond
var defaultConsistentlyDuration = 100 * time.Millisecond
var defaultConsistentlyPollingInterval = 10 * time.Millisecond

// RegisterFailHandler connects Ginkgo to Gomega. When a matcher fails
// the fail handler passed into RegisterFailHandler is called.
func RegisterFailHandler(handler types.GomegaFailHandler) {
	RegisterFailHandlerWithT(testingtsupport.EmptyTWithHelper{}, handler)
}

// RegisterFailHandlerWithT ensures that the given types.TWithHelper and fail handler
// are used globally.
func RegisterFailHandlerWithT(t types.TWithHelper, handler types.GomegaFailHandler) {
	if handler == nil {
		globalFailWrapper = nil
		return
	}

	globalFailWrapper = &types.GomegaFailWrapper{
		Fail:        handler,
		TWithHelper: t,
	}
}

// RegisterTestingT connects Gomega to Golang's XUnit style
// Testing.T tests.  It is now deprecated and you should use NewWithT() instead.
//
// Legacy Documentation:
//
// You'll need to call this at the top of each XUnit style test:
//
//    func TestFarmHasCow(t *testing.T) {
//        RegisterTestingT(t)
//
//        f := farm.New([]string{"Cow", "Horse"})
//        Expect(f.HasCow()).To(BeTrue(), "Farm should have cow")
//    }
//
// Note that this *testing.T is registered *globally* by Gomega (this is why you don't have to
// pass `t` down to the matcher itself).  This means that you cannot run the XUnit style tests
// in parallel as the global fail handler cannot point to more than one testing.T at a time.
//
// NewWithT() does not have this limitation
//
// (As an aside: Ginkgo gets around this limitation by running parallel tests in different *processes*).
func RegisterTestingT(t types.GomegaTestingT) {
	tWithHelper, hasHelper := t.(types.TWithHelper)
	if !hasHelper {
		RegisterFailHandler(testingtsupport.BuildTestingTGomegaFailWrapper(t).Fail)
		return
	}
	RegisterFailHandlerWithT(tWithHelper, testingtsupport.BuildTestingTGomegaFailWrapper(t).Fail)
>>>>>>> 33cbc1d (add batchrelease controller)
}

// InterceptGomegaFailures runs a given callback and returns an array of
// failure messages generated by any Gomega assertions within the callback.
<<<<<<< HEAD
// Exeuction continues after the first failure allowing users to collect all failures
// in the callback.
=======
//
// This is accomplished by temporarily replacing the *global* fail handler
// with a fail handler that simply annotates failures.  The original fail handler
// is reset when InterceptGomegaFailures returns.
>>>>>>> 33cbc1d (add batchrelease controller)
//
// This is most useful when testing custom matchers, but can also be used to check
// on a value using a Gomega assertion without causing a test failure.
func InterceptGomegaFailures(f func()) []string {
<<<<<<< HEAD
	originalHandler := Default.(*internal.Gomega).Fail
	failures := []string{}
	Default.(*internal.Gomega).Fail = func(message string, callerSkip ...int) {
		failures = append(failures, message)
	}
	defer func() {
		Default.(*internal.Gomega).Fail = originalHandler
	}()
	f()
	return failures
}

// InterceptGomegaFailure runs a given callback and returns the first
// failure message generated by any Gomega assertions within the callback, wrapped in an error.
//
// The callback ceases execution as soon as the first failed assertion occurs, however Gomega
// does not register a failure with the FailHandler registered via RegisterFailHandler - it is up
// to the user to decide what to do with the returned error
func InterceptGomegaFailure(f func()) (err error) {
	originalHandler := Default.(*internal.Gomega).Fail
	Default.(*internal.Gomega).Fail = func(message string, callerSkip ...int) {
		err = errors.New(message)
		panic("stop execution")
	}

	defer func() {
		Default.(*internal.Gomega).Fail = originalHandler
		if e := recover(); e != nil {
			if err == nil {
				panic(e)
			}
		}
	}()

	f()
	return err
}

func ensureDefaultGomegaIsConfigured() {
	if !Default.(*internal.Gomega).IsConfigured() {
		panic(nilGomegaPanic)
	}
}

=======
	originalHandler := globalFailWrapper.Fail
	failures := []string{}
	RegisterFailHandler(func(message string, callerSkip ...int) {
		failures = append(failures, message)
	})
	f()
	RegisterFailHandler(originalHandler)
	return failures
}

>>>>>>> 33cbc1d (add batchrelease controller)
// Ω wraps an actual value allowing assertions to be made on it:
//    Ω("foo").Should(Equal("foo"))
//
// If Ω is passed more than one argument it will pass the *first* argument to the matcher.
// All subsequent arguments will be required to be nil/zero.
//
// This is convenient if you want to make an assertion on a method/function that returns
// a value and an error - a common patter in Go.
//
// For example, given a function with signature:
//    func MyAmazingThing() (int, error)
//
// Then:
//    Ω(MyAmazingThing()).Should(Equal(3))
// Will succeed only if `MyAmazingThing()` returns `(3, nil)`
//
// Ω and Expect are identical
func Ω(actual interface{}, extra ...interface{}) Assertion {
<<<<<<< HEAD
	ensureDefaultGomegaIsConfigured()
	return Default.Ω(actual, extra...)
=======
	return ExpectWithOffset(0, actual, extra...)
>>>>>>> 33cbc1d (add batchrelease controller)
}

// Expect wraps an actual value allowing assertions to be made on it:
//    Expect("foo").To(Equal("foo"))
//
// If Expect is passed more than one argument it will pass the *first* argument to the matcher.
// All subsequent arguments will be required to be nil/zero.
//
// This is convenient if you want to make an assertion on a method/function that returns
// a value and an error - a common patter in Go.
//
// For example, given a function with signature:
//    func MyAmazingThing() (int, error)
//
// Then:
//    Expect(MyAmazingThing()).Should(Equal(3))
// Will succeed only if `MyAmazingThing()` returns `(3, nil)`
//
// Expect and Ω are identical
func Expect(actual interface{}, extra ...interface{}) Assertion {
<<<<<<< HEAD
	ensureDefaultGomegaIsConfigured()
	return Default.Expect(actual, extra...)
=======
	return ExpectWithOffset(0, actual, extra...)
>>>>>>> 33cbc1d (add batchrelease controller)
}

// ExpectWithOffset wraps an actual value allowing assertions to be made on it:
//    ExpectWithOffset(1, "foo").To(Equal("foo"))
//
// Unlike `Expect` and `Ω`, `ExpectWithOffset` takes an additional integer argument
<<<<<<< HEAD
// that is used to modify the call-stack offset when computing line numbers. It is
// the same as `Expect(...).WithOffset`.
=======
// that is used to modify the call-stack offset when computing line numbers.
>>>>>>> 33cbc1d (add batchrelease controller)
//
// This is most useful in helper functions that make assertions.  If you want Gomega's
// error message to refer to the calling line in the test (as opposed to the line in the helper function)
// set the first argument of `ExpectWithOffset` appropriately.
func ExpectWithOffset(offset int, actual interface{}, extra ...interface{}) Assertion {
<<<<<<< HEAD
	ensureDefaultGomegaIsConfigured()
	return Default.ExpectWithOffset(offset, actual, extra...)
}

/*
Eventually enables making assertions on asynchronous behavior.

Eventually checks that an assertion *eventually* passes.  Eventually blocks when called and attempts an assertion periodically until it passes or a timeout occurs.  Both the timeout and polling interval are configurable as optional arguments.
The first optional argument is the timeout (which defaults to 1s), the second is the polling interval (which defaults to 10ms).  Both intervals can be specified as time.Duration, parsable duration strings or floats/integers (in which case they are interpreted as seconds).

Eventually works with any Gomega compatible matcher and supports making assertions against three categories of actual value:

**Category 1: Making Eventually assertions on values**

There are several examples of values that can change over time.  These can be passed in to Eventually and will be passed to the matcher repeatedly until a match occurs.  For example:

    c := make(chan bool)
    go DoStuff(c)
    Eventually(c, "50ms").Should(BeClosed())

will poll the channel repeatedly until it is closed.  In this example `Eventually` will block until either the specified timeout of 50ms has elapsed or the channel is closed, whichever comes first.

Several Gomega libraries allow you to use Eventually in this way.  For example, the gomega/gexec package allows you to block until a *gexec.Session exits successfuly via:

    Eventually(session).Should(gexec.Exit(0))

And the gomega/gbytes package allows you to monitor a streaming *gbytes.Buffer until a given string is seen:

	Eventually(buffer).Should(gbytes.Say("hello there"))

In these examples, both `session` and `buffer` are designed to be thread-safe when polled by the `Exit` and `Say` matchers.  This is not true in general of most raw values, so while it is tempting to do something like:

	// THIS IS NOT THREAD-SAFE
	var s *string
	go mutateStringEventually(s)
	Eventually(s).Should(Equal("I've changed"))

this will trigger Go's race detector as the goroutine polling via Eventually will race over the value of s with the goroutine mutating the string.  For cases like this you can use channels or introduce your own locking around s by passing Eventually a function.

**Category 2: Make Eventually assertions on functions**

Eventually can be passed functions that **take no arguments** and **return at least one value**.  When configured this way, Eventually will poll the function repeatedly and pass the first returned value to the matcher.

For example:

    Eventually(func() int {
    	return client.FetchCount()
    }).Should(BeNumerically(">=", 17))

 will repeatedly poll client.FetchCount until the BeNumerically matcher is satisfied.  (Note that this example could have been written as Eventually(client.FetchCount).Should(BeNumerically(">=", 17)))

If multple values are returned by the function, Eventually will pass the first value to the matcher and require that all others are zero-valued.  This allows you to pass Eventually a function that returns a value and an error - a common patternin Go.

For example, consider a method that returns a value and an error:
    func FetchFromDB() (string, error)

Then
    Eventually(FetchFromDB).Should(Equal("got it"))

will pass only if and when the returned error is nil *and* the returned string satisfies the matcher.

It is important to note that the function passed into Eventually is invoked *synchronously* when polled.  Eventually does not (in fact, it cannot) kill the function if it takes longer to return than Eventually's configured timeout.  You should design your functions with this in mind.

**Category 3: Making assertions _in_ the function passed into Eventually**

When testing complex systems it can be valuable to assert that a _set_ of assertions passes Eventually.  Eventually supports this by accepting functions that take a single Gomega argument and return zero or more values.

Here's an example that makes some asssertions and returns a value and error:

	Eventually(func(g Gomega) (Widget, error) {
		ids, err := client.FetchIDs()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ids).To(ContainElement(1138))
		return client.FetchWidget(1138)
	}).Should(Equal(expectedWidget))

will pass only if all the assertions in the polled function pass and the return value satisfied the matcher.

Eventually also supports a special case polling function that takes a single Gomega argument and returns no values.  Eventually assumes such a function is making assertions and is designed to work with the Succeed matcher to validate that all assertions have passed.
For example:

    Eventually(func(g Gomega) {
    	model, err := client.Find(1138)
    	g.Expect(err).NotTo(HaveOccurred())
    	g.Expect(model.Reticulate()).To(Succeed())
    	g.Expect(model.IsReticulated()).To(BeTrue())
    	g.Expect(model.Save()).To(Succeed())
    }).Should(Succeed())

will rerun the function until all assertions pass.

`Eventually` specifying a timeout interval (and an optional polling interval) are
the same as `Eventually(...).WithTimeout` or `Eventually(...).WithTimeout(...).WithPolling`.
*/
func Eventually(actual interface{}, intervals ...interface{}) AsyncAssertion {
	ensureDefaultGomegaIsConfigured()
	return Default.Eventually(actual, intervals...)
=======
	if globalFailWrapper == nil {
		panic(nilFailHandlerPanic)
	}
	return assertion.New(actual, globalFailWrapper, offset, extra...)
}

// Eventually wraps an actual value allowing assertions to be made on it.
// The assertion is tried periodically until it passes or a timeout occurs.
//
// Both the timeout and polling interval are configurable as optional arguments:
// The first optional argument is the timeout
// The second optional argument is the polling interval
//
// Both intervals can either be specified as time.Duration, parsable duration strings or as floats/integers.  In the
// last case they are interpreted as seconds.
//
// If Eventually is passed an actual that is a function taking no arguments and returning at least one value,
// then Eventually will call the function periodically and try the matcher against the function's first return value.
//
// Example:
//
//    Eventually(func() int {
//        return thingImPolling.Count()
//    }).Should(BeNumerically(">=", 17))
//
// Note that this example could be rewritten:
//
//    Eventually(thingImPolling.Count).Should(BeNumerically(">=", 17))
//
// If the function returns more than one value, then Eventually will pass the first value to the matcher and
// assert that all other values are nil/zero.
// This allows you to pass Eventually a function that returns a value and an error - a common pattern in Go.
//
// For example, consider a method that returns a value and an error:
//    func FetchFromDB() (string, error)
//
// Then
//    Eventually(FetchFromDB).Should(Equal("hasselhoff"))
//
// Will pass only if the the returned error is nil and the returned string passes the matcher.
//
// Eventually's default timeout is 1 second, and its default polling interval is 10ms
func Eventually(actual interface{}, intervals ...interface{}) AsyncAssertion {
	return EventuallyWithOffset(0, actual, intervals...)
>>>>>>> 33cbc1d (add batchrelease controller)
}

// EventuallyWithOffset operates like Eventually but takes an additional
// initial argument to indicate an offset in the call stack.  This is useful when building helper
// functions that contain matchers.  To learn more, read about `ExpectWithOffset`.
<<<<<<< HEAD
//
// `EventuallyWithOffset` is the same as `Eventually(...).WithOffset`.
//
// `EventuallyWithOffset` specifying a timeout interval (and an optional polling interval) are
// the same as `Eventually(...).WithOffset(...).WithTimeout` or
// `Eventually(...).WithOffset(...).WithTimeout(...).WithPolling`.
func EventuallyWithOffset(offset int, actual interface{}, intervals ...interface{}) AsyncAssertion {
	ensureDefaultGomegaIsConfigured()
	return Default.EventuallyWithOffset(offset, actual, intervals...)
}

/*
Consistently, like Eventually, enables making assertions on asynchronous behavior.

Consistently blocks when called for a specified duration.  During that duration Consistently repeatedly polls its matcher and ensures that it is satisfied.  If the matcher is consistently satisfied, then Consistently will pass.  Otherwise Consistently will fail.

Both the total waiting duration and the polling interval are configurable as optional arguments.  The first optional arugment is the duration that Consistently will run for (defaults to 100ms), and the second argument is the polling interval (defaults to 10ms).  As with Eventually, these intervals can be passed in as time.Duration, parsable duration strings or an integer or float number of seconds.

Consistently accepts the same three categories of actual as Eventually, check the Eventually docs to learn more.

Consistently is useful in cases where you want to assert that something *does not happen* for a period of time.  For example, you may want to assert that a goroutine does *not* send data down a channel.  In this case you could write:

    Consistently(channel, "200ms").ShouldNot(Receive())

This will block for 200 milliseconds and repeatedly check the channel and ensure nothing has been received.
*/
func Consistently(actual interface{}, intervals ...interface{}) AsyncAssertion {
	ensureDefaultGomegaIsConfigured()
	return Default.Consistently(actual, intervals...)
=======
func EventuallyWithOffset(offset int, actual interface{}, intervals ...interface{}) AsyncAssertion {
	if globalFailWrapper == nil {
		panic(nilFailHandlerPanic)
	}
	timeoutInterval := defaultEventuallyTimeout
	pollingInterval := defaultEventuallyPollingInterval
	if len(intervals) > 0 {
		timeoutInterval = toDuration(intervals[0])
	}
	if len(intervals) > 1 {
		pollingInterval = toDuration(intervals[1])
	}
	return asyncassertion.New(asyncassertion.AsyncAssertionTypeEventually, actual, globalFailWrapper, timeoutInterval, pollingInterval, offset)
}

// Consistently wraps an actual value allowing assertions to be made on it.
// The assertion is tried periodically and is required to pass for a period of time.
//
// Both the total time and polling interval are configurable as optional arguments:
// The first optional argument is the duration that Consistently will run for
// The second optional argument is the polling interval
//
// Both intervals can either be specified as time.Duration, parsable duration strings or as floats/integers.  In the
// last case they are interpreted as seconds.
//
// If Consistently is passed an actual that is a function taking no arguments and returning at least one value,
// then Consistently will call the function periodically and try the matcher against the function's first return value.
//
// If the function returns more than one value, then Consistently will pass the first value to the matcher and
// assert that all other values are nil/zero.
// This allows you to pass Consistently a function that returns a value and an error - a common pattern in Go.
//
// Consistently is useful in cases where you want to assert that something *does not happen* over a period of time.
// For example, you want to assert that a goroutine does *not* send data down a channel.  In this case, you could:
//
//   Consistently(channel).ShouldNot(Receive())
//
// Consistently's default duration is 100ms, and its default polling interval is 10ms
func Consistently(actual interface{}, intervals ...interface{}) AsyncAssertion {
	return ConsistentlyWithOffset(0, actual, intervals...)
>>>>>>> 33cbc1d (add batchrelease controller)
}

// ConsistentlyWithOffset operates like Consistently but takes an additional
// initial argument to indicate an offset in the call stack. This is useful when building helper
// functions that contain matchers. To learn more, read about `ExpectWithOffset`.
<<<<<<< HEAD
//
// `ConsistentlyWithOffset` is the same as `Consistently(...).WithOffset` and
// optional `WithTimeout` and `WithPolling`.
func ConsistentlyWithOffset(offset int, actual interface{}, intervals ...interface{}) AsyncAssertion {
	ensureDefaultGomegaIsConfigured()
	return Default.ConsistentlyWithOffset(offset, actual, intervals...)
=======
func ConsistentlyWithOffset(offset int, actual interface{}, intervals ...interface{}) AsyncAssertion {
	if globalFailWrapper == nil {
		panic(nilFailHandlerPanic)
	}
	timeoutInterval := defaultConsistentlyDuration
	pollingInterval := defaultConsistentlyPollingInterval
	if len(intervals) > 0 {
		timeoutInterval = toDuration(intervals[0])
	}
	if len(intervals) > 1 {
		pollingInterval = toDuration(intervals[1])
	}
	return asyncassertion.New(asyncassertion.AsyncAssertionTypeConsistently, actual, globalFailWrapper, timeoutInterval, pollingInterval, offset)
>>>>>>> 33cbc1d (add batchrelease controller)
}

// SetDefaultEventuallyTimeout sets the default timeout duration for Eventually. Eventually will repeatedly poll your condition until it succeeds, or until this timeout elapses.
func SetDefaultEventuallyTimeout(t time.Duration) {
<<<<<<< HEAD
	Default.SetDefaultEventuallyTimeout(t)
=======
	defaultEventuallyTimeout = t
>>>>>>> 33cbc1d (add batchrelease controller)
}

// SetDefaultEventuallyPollingInterval sets the default polling interval for Eventually.
func SetDefaultEventuallyPollingInterval(t time.Duration) {
<<<<<<< HEAD
	Default.SetDefaultEventuallyPollingInterval(t)
=======
	defaultEventuallyPollingInterval = t
>>>>>>> 33cbc1d (add batchrelease controller)
}

// SetDefaultConsistentlyDuration sets  the default duration for Consistently. Consistently will verify that your condition is satisfied for this long.
func SetDefaultConsistentlyDuration(t time.Duration) {
<<<<<<< HEAD
	Default.SetDefaultConsistentlyDuration(t)
=======
	defaultConsistentlyDuration = t
>>>>>>> 33cbc1d (add batchrelease controller)
}

// SetDefaultConsistentlyPollingInterval sets the default polling interval for Consistently.
func SetDefaultConsistentlyPollingInterval(t time.Duration) {
<<<<<<< HEAD
	Default.SetDefaultConsistentlyPollingInterval(t)
=======
	defaultConsistentlyPollingInterval = t
>>>>>>> 33cbc1d (add batchrelease controller)
}

// AsyncAssertion is returned by Eventually and Consistently and polls the actual value passed into Eventually against
// the matcher passed to the Should and ShouldNot methods.
//
// Both Should and ShouldNot take a variadic optionalDescription argument.
// This argument allows you to make your failure messages more descriptive.
// If a single argument of type `func() string` is passed, this function will be lazily evaluated if a failure occurs
// and the returned string is used to annotate the failure message.
// Otherwise, this argument is passed on to fmt.Sprintf() and then used to annotate the failure message.
//
// Both Should and ShouldNot return a boolean that is true if the assertion passed and false if it failed.
//
// Example:
//
//   Eventually(myChannel).Should(Receive(), "Something should have come down the pipe.")
//   Consistently(myChannel).ShouldNot(Receive(), func() string { return "Nothing should have come down the pipe." })
<<<<<<< HEAD
type AsyncAssertion = types.AsyncAssertion

// GomegaAsyncAssertion is deprecated in favor of AsyncAssertion, which does not stutter.
type GomegaAsyncAssertion = types.AsyncAssertion
=======
type AsyncAssertion interface {
	Should(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool
	ShouldNot(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool
}

// GomegaAsyncAssertion is deprecated in favor of AsyncAssertion, which does not stutter.
type GomegaAsyncAssertion = AsyncAssertion
>>>>>>> 33cbc1d (add batchrelease controller)

// Assertion is returned by Ω and Expect and compares the actual value to the matcher
// passed to the Should/ShouldNot and To/ToNot/NotTo methods.
//
// Typically Should/ShouldNot are used with Ω and To/ToNot/NotTo are used with Expect
// though this is not enforced.
//
// All methods take a variadic optionalDescription argument.
// This argument allows you to make your failure messages more descriptive.
// If a single argument of type `func() string` is passed, this function will be lazily evaluated if a failure occurs
// and the returned string is used to annotate the failure message.
// Otherwise, this argument is passed on to fmt.Sprintf() and then used to annotate the failure message.
//
// All methods return a bool that is true if the assertion passed and false if it failed.
//
// Example:
//
//    Ω(farm.HasCow()).Should(BeTrue(), "Farm %v should have a cow", farm)
<<<<<<< HEAD
type Assertion = types.Assertion

// GomegaAssertion is deprecated in favor of Assertion, which does not stutter.
type GomegaAssertion = types.Assertion

// OmegaMatcher is deprecated in favor of the better-named and better-organized types.GomegaMatcher but sticks around to support existing code that uses it
type OmegaMatcher = types.GomegaMatcher
=======
type Assertion interface {
	Should(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool
	ShouldNot(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool

	To(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool
	ToNot(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool
	NotTo(matcher types.GomegaMatcher, optionalDescription ...interface{}) bool
}

// GomegaAssertion is deprecated in favor of Assertion, which does not stutter.
type GomegaAssertion = Assertion

// OmegaMatcher is deprecated in favor of the better-named and better-organized types.GomegaMatcher but sticks around to support existing code that uses it
type OmegaMatcher types.GomegaMatcher

// WithT wraps a *testing.T and provides `Expect`, `Eventually`, and `Consistently` methods.  This allows you to leverage
// Gomega's rich ecosystem of matchers in standard `testing` test suites.
//
// Use `NewWithT` to instantiate a `WithT`
type WithT struct {
	t types.GomegaTestingT
}

// GomegaWithT is deprecated in favor of gomega.WithT, which does not stutter.
type GomegaWithT = WithT

// NewWithT takes a *testing.T and returngs a `gomega.WithT` allowing you to use `Expect`, `Eventually`, and `Consistently` along with
// Gomega's rich ecosystem of matchers in standard `testing` test suits.
//
//    func TestFarmHasCow(t *testing.T) {
//        g := gomega.NewWithT(t)
//
//        f := farm.New([]string{"Cow", "Horse"})
//        g.Expect(f.HasCow()).To(BeTrue(), "Farm should have cow")
//     }
func NewWithT(t types.GomegaTestingT) *WithT {
	return &WithT{
		t: t,
	}
}

// NewGomegaWithT is deprecated in favor of gomega.NewWithT, which does not stutter.
func NewGomegaWithT(t types.GomegaTestingT) *GomegaWithT {
	return NewWithT(t)
}

// ExpectWithOffset is used to make assertions. See documentation for ExpectWithOffset.
func (g *WithT) ExpectWithOffset(offset int, actual interface{}, extra ...interface{}) Assertion {
	return assertion.New(actual, testingtsupport.BuildTestingTGomegaFailWrapper(g.t), offset, extra...)
}

// EventuallyWithOffset is used to make asynchronous assertions. See documentation for EventuallyWithOffset.
func (g *WithT) EventuallyWithOffset(offset int, actual interface{}, intervals ...interface{}) AsyncAssertion {
	timeoutInterval := defaultEventuallyTimeout
	pollingInterval := defaultEventuallyPollingInterval
	if len(intervals) > 0 {
		timeoutInterval = toDuration(intervals[0])
	}
	if len(intervals) > 1 {
		pollingInterval = toDuration(intervals[1])
	}
	return asyncassertion.New(asyncassertion.AsyncAssertionTypeEventually, actual, testingtsupport.BuildTestingTGomegaFailWrapper(g.t), timeoutInterval, pollingInterval, offset)
}

// ConsistentlyWithOffset is used to make asynchronous assertions. See documentation for ConsistentlyWithOffset.
func (g *WithT) ConsistentlyWithOffset(offset int, actual interface{}, intervals ...interface{}) AsyncAssertion {
	timeoutInterval := defaultConsistentlyDuration
	pollingInterval := defaultConsistentlyPollingInterval
	if len(intervals) > 0 {
		timeoutInterval = toDuration(intervals[0])
	}
	if len(intervals) > 1 {
		pollingInterval = toDuration(intervals[1])
	}
	return asyncassertion.New(asyncassertion.AsyncAssertionTypeConsistently, actual, testingtsupport.BuildTestingTGomegaFailWrapper(g.t), timeoutInterval, pollingInterval, offset)
}

// Expect is used to make assertions. See documentation for Expect.
func (g *WithT) Expect(actual interface{}, extra ...interface{}) Assertion {
	return g.ExpectWithOffset(0, actual, extra...)
}

// Eventually is used to make asynchronous assertions. See documentation for Eventually.
func (g *WithT) Eventually(actual interface{}, intervals ...interface{}) AsyncAssertion {
	return g.EventuallyWithOffset(0, actual, intervals...)
}

// Consistently is used to make asynchronous assertions. See documentation for Consistently.
func (g *WithT) Consistently(actual interface{}, intervals ...interface{}) AsyncAssertion {
	return g.ConsistentlyWithOffset(0, actual, intervals...)
}

func toDuration(input interface{}) time.Duration {
	duration, ok := input.(time.Duration)
	if ok {
		return duration
	}

	value := reflect.ValueOf(input)
	kind := reflect.TypeOf(input).Kind()

	if reflect.Int <= kind && kind <= reflect.Int64 {
		return time.Duration(value.Int()) * time.Second
	} else if reflect.Uint <= kind && kind <= reflect.Uint64 {
		return time.Duration(value.Uint()) * time.Second
	} else if reflect.Float32 <= kind && kind <= reflect.Float64 {
		return time.Duration(value.Float() * float64(time.Second))
	} else if reflect.String == kind {
		duration, err := time.ParseDuration(value.String())
		if err != nil {
			panic(fmt.Sprintf("%#v is not a valid parsable duration string.", input))
		}
		return duration
	}

	panic(fmt.Sprintf("%v is not a valid interval.  Must be time.Duration, parsable duration string or a number.", input))
}

// Gomega describes the essential Gomega DSL. This interface allows libraries
// to abstract between the standard package-level function implementations
// and alternatives like *WithT.
type Gomega interface {
	Expect(actual interface{}, extra ...interface{}) Assertion
	Eventually(actual interface{}, intervals ...interface{}) AsyncAssertion
	Consistently(actual interface{}, intervals ...interface{}) AsyncAssertion
}

type globalFailHandlerGomega struct{}

// DefaultGomega supplies the standard package-level implementation
var Default Gomega = globalFailHandlerGomega{}

// Expect is used to make assertions. See documentation for Expect.
func (globalFailHandlerGomega) Expect(actual interface{}, extra ...interface{}) Assertion {
	return Expect(actual, extra...)
}

// Eventually is used to make asynchronous assertions. See documentation for Eventually.
func (globalFailHandlerGomega) Eventually(actual interface{}, extra ...interface{}) AsyncAssertion {
	return Eventually(actual, extra...)
}

// Consistently is used to make asynchronous assertions. See documentation for Consistently.
func (globalFailHandlerGomega) Consistently(actual interface{}, extra ...interface{}) AsyncAssertion {
	return Consistently(actual, extra...)
}
>>>>>>> 33cbc1d (add batchrelease controller)
