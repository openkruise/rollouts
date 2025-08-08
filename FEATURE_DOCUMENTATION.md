# Canary Batch Timing Feature

## Overview

This feature adds the ability to track start and end times for every canary batch/step in Rollout resources. This addresses issue #112 where users previously could not know the start and end times for each canary batch.

## Changes Made

### 1. API Changes

#### BatchReleaseCanaryStatus (v1alpha1 and v1beta1)
Added `BatchStartTime` field to track when each batch starts processing:

```go
type BatchReleaseCanaryStatus struct {
    // ... existing fields ...
    
    // BatchStartTime is the start timestamp of the current batch.
    // This field is updated when a batch starts processing.
    // +optional
    BatchStartTime *metav1.Time `json:"batchStartTime,omitempty"`
    
    // BatchReadyTime is the ready timestamp of the current batch or the last batch.
    // This field is updated once a batch ready, and the batches[x].pausedSeconds
    // relies on this field to calculate the real-time duration.
    BatchReadyTime *metav1.Time `json:"batchReadyTime,omitempty"`
    
    // ... other fields ...
}
```

#### Rollout CanaryStatus (v1beta1)
Added timing fields to track step timing information:

```go
type CanaryStatus struct {
    // ... existing fields ...
    
    // CurrentStepStartTime is the start timestamp of the current canary step/batch.
    // This field is updated when a step starts processing.
    // +optional
    CurrentStepStartTime *metav1.Time `json:"currentStepStartTime,omitempty"`
    
    // CurrentStepEndTime is the end timestamp of the current canary step/batch.
    // This field is updated when a step completes.
    // +optional
    CurrentStepEndTime *metav1.Time `json:"currentStepEndTime,omitempty"`
    
    // ... other fields ...
}
```

### 2. Controller Logic Changes

#### BatchRelease Executor
- **Setting Start Time**: `BatchStartTime` is set when a batch first enters `UpgradingBatchState`
- **Resetting Start Time**: `BatchStartTime` is reset to `nil` when:
  - Moving to the next batch
  - Restarting a batch
  - Recalculating due to plan changes

#### Rollout Controller
- **Syncing Timing**: The `syncBatchRelease` function now syncs timing information from `BatchRelease` to `Rollout` status
- **Status Updates**: `CurrentStepStartTime` and `CurrentStepEndTime` are updated based on the corresponding `BatchRelease` timing

### 3. Conversion Functions
Updated conversion functions in `api/v1alpha1/conversion.go` to properly handle the new `BatchStartTime` field when converting between API versions.

## Usage

### Viewing Timing Information

You can now view timing information for canary batches:

```bash
# View BatchRelease timing
kubectl get batchrelease <name> -o yaml

# View Rollout timing
kubectl get rollout <name> -o yaml
```

### Example Output

```yaml
status:
  canaryStatus:
    currentBatch: 1
    batchState: "UpgradingBatchState"
    batchStartTime: "2023-01-06T10:30:00Z"
    batchReadyTime: "2023-01-06T10:35:00Z"
    updatedReplicas: 2
    updatedReadyReplicas: 2
```

## Benefits

1. **Visibility**: Users can now track how long each canary batch takes to complete
2. **Monitoring**: Enables better monitoring and alerting based on batch timing
3. **Debugging**: Helps identify performance issues or bottlenecks in specific batches
4. **Compliance**: Provides audit trail for deployment timing

## Backward Compatibility

- The new fields are optional (`+optional` tag)
- Existing Rollouts and BatchReleases will continue to work without the new timing fields
- The fields will be populated for new deployments or when existing resources are updated

## Testing

The feature has been tested with:
- Unit tests for the batchrelease controller
- API validation and conversion tests
- Build verification to ensure no compilation errors

## Related Issues

- Fixes #112: [Feature] need record the starttime and endtime for every canary batch in Rollout
