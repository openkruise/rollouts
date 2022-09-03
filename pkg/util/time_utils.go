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

const (
	DateTimeZoneLayout = "2006-01-02 15:04:05 MST"
	DateTimeLayout     = "2006-01-02 15:04:05"
	DateLayout         = "2006-01-02"
)

//ValidateTime used to validate _time whether right
func ValidateTime(date, _time string, zone *time.Location) (time.Time, error) {
	if zone == nil {
		zone = time.Local
	}
	if date == "" {
		date = DateLayout
	}
	return time.ParseInLocation(DateTimeLayout, fmt.Sprintf("%s %s", date, _time), zone)
}

func TimeZone(zone *rolloutv1alpha1.TimeZone) *time.Location {
	if zone != nil {
		return time.FixedZone(zone.Name, zone.Offset)
	}
	return time.Local
}

//TimeInSlice used to validate the expectedTime whether in the timeSlices.
//it returns expectedTime and 'false' if the timeSlices is wrong,so you have to make sure the time Slice is correct.
//it returns expectedTime and 'true'  if the expectedTime is in this timeSlices.
//it returns adjacent time  and 'false'  if the expectedTime is not in this timeSlices.
func TimeInSlice(expectedTime time.Time, allowRunTime *rolloutv1alpha1.AllowRunTime) (time.Time, bool) {
	if allowRunTime == nil {
		return expectedTime, true
	}
	if len(allowRunTime.TimeSlices) == 0 {
		return expectedTime, true
	}
	var (
		err    error
		start  time.Time
		end    time.Time
		minSub = time.Hour * 48
	)

	date := expectedTime.Format(DateLayout)
	timeZone := TimeZone(allowRunTime.TimeZone)

	for i, timeSlice := range allowRunTime.TimeSlices {
		start, err = ValidateTime(date, timeSlice.StartTime, timeZone)
		if err != nil {
			klog.V(5).Infof("timeSlices[%d] StartTime is err %s", i, err.Error())
			return expectedTime, false
		}
		end, err = ValidateTime(date, timeSlice.EndTime, timeZone)
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
