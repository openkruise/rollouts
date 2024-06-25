local function updateOrCreateSubset(subsets, name, labels)
    for _, subset in ipairs(subsets) do
        if subset.name == name then
            if next(labels) ~= nil then
                subset.labels = subset.labels or {}
                for key, value in pairs(labels) do
                    subset.labels[key] = value
                end
            end
            return -- Do not need to continue if name exists,as we update the first occurrence
        end
    end
    table.insert(subsets, {
        name = name,
        labels = next(labels) ~= nil and labels or nil
    })
end

local spec = obj.data.spec
local pod_label_key = obj.revisionLabelKey
if spec.subsets == nil then
    spec.subsets = {}
end

local stable_labels = {}
if obj.stableRevision ~= nil and obj.stableRevision ~= "" then
    stable_labels[pod_label_key] = obj.stableRevision
end

local canary_labels = {}
if obj.canaryRevision ~= nil and obj.canaryRevision ~= "" then
    canary_labels[pod_label_key] = obj.canaryRevision
end

-- Process stable subset
updateOrCreateSubset(spec.subsets, obj.stableName, stable_labels)

-- Process canary subset
updateOrCreateSubset(spec.subsets, obj.canaryName, canary_labels)

return obj.data
