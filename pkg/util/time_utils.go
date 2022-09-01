/*
Copyright 2022 The KubePort Authors.
*/

package util

import (
	"fmt"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"k8s.io/klog/v2"
	"time"
)

//ValidateTime used to validate _time whether right
func ValidateTime(date, _time string) (time.Time, error) {
	layout := "2006-01-02 15:04:05"
	if date == "" {
		date = "2022-08-21"
	}
	return time.ParseInLocation(layout, fmt.Sprintf("%s %s", date, _time), time.Local)
}

//TimeInSlice used to validate the expectedTime whether in the timeSlices.
//it returns expectedTime and 'false' if the timeSlices is wrong,so you have to make sure the Time Slice is correct.
//it returns expectedTime and 'true'  if the expectedTime is in this timeSlices.
//it returns adjacent time  and 'false'  if the expectedTime is not in this timeSlices.
func TimeInSlice(expectedTime time.Time, timeSlices []rolloutv1alpha1.TimeSlice) (time.Time, bool) {
	date := expectedTime.Format("2006-01-02")
	if len(timeSlices) == 0 {
		return expectedTime, true
	}
	var (
		err    error
		start  time.Time
		end    time.Time
		minSub = time.Hour * 48
	)
	for i, timeSlice := range timeSlices {
		start, err = ValidateTime(date, timeSlice.StartTime)
		if err != nil {
			klog.V(5).Infof("timeSlices[%d] StartTime is err %s", i, err.Error())
			return expectedTime, false
		}
		end, err = ValidateTime(date, timeSlice.EndTime)
		if err != nil {
			klog.V(5).Infof("timeSlices[%d] EndTime is err %s", i, err.Error())
			return expectedTime, false
		}

		if expectedTime.After(start) && expectedTime.Before(end) {
			return expectedTime, true
		}

		subTime := start.Sub(expectedTime)
		if subTime < 0 {
			subTime += time.Hour * 24
		}
		if subTime < minSub {
			minSub = subTime
		}
	}
	return expectedTime.Add(minSub), false
}
