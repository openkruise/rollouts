package v1alpha1

const (
	// ControllerRevisionHashLabelKey is used to record the controller revision of current resource.
	ControllerRevisionHashLabelKey = "apps.kruise.io/controller-revision-hash"

	// SubSetNameLabelKey is used to record the name of current subset.
	SubSetNameLabelKey = "apps.kruise.io/subset-name"

	// SpecifiedDeleteKey indicates this object should be deleted, and the value could be the deletion option.
	SpecifiedDeleteKey = "apps.kruise.io/specified-delete"
)
