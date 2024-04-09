local spec = obj.data.spec
local podLabelKey = obj.revisionLabelKey

if spec.subsets == nil then
    spec.subsets = {}
end

-- selector lables might come from pod-template-hash and patchPodTemplateMetadata
-- now we only support pod-template-hash
local stableLabels = {}
if obj.stableRevision ~= nil and obj.stableRevision ~= "" then
    stableLabels[podLabelKey] = obj.stableRevision
end
local canaryLabels = {}
if obj.canaryRevision ~= nil and obj.canaryRevision ~= "" then
    canaryLabels[podLabelKey] = obj.canaryRevision
end
local StableNameAlreadyExist = false

-- if stableName already exists, just appened the lables
for _, subset in ipairs(spec.subsets) do
    if subset.name == obj.stableName then
        StableNameAlreadyExist = true
        if next(stableLabels) ~= nil then
            subset.labels = subset.labels or {}
            for key, value in pairs(stableLabels) do
                subset.labels[key] = value
            end
        end
    end
end
-- if stableName doesn't exist, create it and its labels
if not StableNameAlreadyExist then
    local stable = {}
    stable.name = obj.stableName
    if next(stableLabels) ~= nil then
        stable.labels = stableLabels
    end
    table.insert(spec.subsets, stable)
end

-- Aussue the canaryName never exist, create it and its labels
local canary = {}
canary.name = obj.canaryName
if next(canaryLabels) ~= nil then
    canary.labels = canaryLabels
end
table.insert(spec.subsets, canary)


return obj.data