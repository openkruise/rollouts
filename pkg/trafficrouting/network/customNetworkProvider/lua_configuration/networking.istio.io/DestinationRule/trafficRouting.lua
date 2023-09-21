local spec = obj.data.spec
local canary = {}
canary.labels = {}
canary.name = "canary"
local podLabelKey = "istio.service.tag"
canary.labels[podLabelKey] = "gray"
table.insert(spec.subsets, canary)
return obj.data