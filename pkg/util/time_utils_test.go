/*
Copyright 2022 The KubePort Authors.
*/

package util

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"testing"
	"time"
)

var allowTime = &rolloutv1alpha1.AllowRunTime{
	TimeZone: nil,
	TimeSlices: []rolloutv1alpha1.TimeSlice{
		{
			StartTime: "00:00:00",
			EndTime:   "2:00:00",
		},
		{
			StartTime: "10:00:00",
			EndTime:   "12:00:00",
		},
		{
			StartTime: "16:00:00",
			EndTime:   "20:00:00",
		},
	},
}

func TestTimeInSlice(t *testing.T) {
	RegisterFailHandler(Fail)
	test := []struct {
		Name         string
		TestTime     string
		ExpectedTime string
		ExpectedRes  bool
	}{
		{
			Name:         "in current slice",
			TestTime:     "2022-08-08 1:03:03",
			ExpectedTime: "2022-08-08 1:03:03",
			ExpectedRes:  true,
		},
		{
			Name:         "in current day",
			TestTime:     "2022-08-08 13:00:00",
			ExpectedTime: "2022-08-08 16:00:00",
			ExpectedRes:  false,
		},
		{
			Name:         "in next day",
			TestTime:     "2022-08-08 22:03:03",
			ExpectedTime: "2022-08-09 00:00:00",
			ExpectedRes:  false,
		},
	}

	for _, s := range test {
		t.Run(s.Name, func(t *testing.T) {
			testTime, _ := time.ParseInLocation(DateTimeLayout, s.TestTime, time.Local)
			expectedTime, _ := time.ParseInLocation(DateTimeLayout, s.ExpectedTime, time.Local)
			resTime, res := TimeInSlice(testTime, allowTime)
			Expect(expectedTime.Unix()).Should(Equal(resTime.Unix()))
			Expect(s.ExpectedRes).Should(Equal(res))
		})
	}
}

func TestValidateTime(t *testing.T) {
	RegisterFailHandler(Fail)
	test := []struct {
		name   string
		date   string
		time   string
		expect string
	}{
		{
			name:   "right: date not empty",
			date:   "2022-08-08",
			time:   "00:00:00",
			expect: "2022-08-08 00:00:00",
		},
		{
			name:   "right: date is empty",
			date:   "",
			time:   "01:00:00",
			expect: "2006-01-02 01:00:00",
		},
		{
			name: "wrong: time more then 24h",
			date: "",
			time: "25:00:00",
		},
		{
			name: "wrong: time less then 0h",
			date: "",
			time: "-01:00:00",
		},
		{
			name: "wrong: time is incomplete",
			date: "",
			time: "21:00",
		},
	}
	for _, s := range test {
		t.Run(s.name, func(t *testing.T) {
			resTime, err := ValidateTime(s.date, s.time, nil)
			if s.expect != "" {
				expectedTime, _ := time.ParseInLocation(DateTimeLayout, s.expect, time.Local)
				Expect(expectedTime.Unix()).Should(Equal(resTime.Unix()))
			} else {
				Expect(len(err.Error()) != 0).Should(BeTrue())
				t.Log(err)
			}
		})
	}
}
