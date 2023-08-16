local spec = obj.data.spec
local canary = {}
canary.labels = {}
canary.name = "canary"
for k, v in pairs(obj.patchPodMetadata.labels) do
    canary.labels[k] = v
end
table.insert(spec.subsets, canary)
return obj.data