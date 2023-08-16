-- obj = {
--     data = {
--         spec = {
--             host = "reviews.prod.svc.cluster.local",
--             subsets = {
--                 {
--                     name = "v1",
--                     labels = {
--                         version = "v1",
--                     }
                        
--                 },
--                 {
--                     name = "v2",
--                     labels = {
--                         version = "v2",
--                     }
--                 }
--             }
--         }
--     },
--     patchPodMetadata = {
--         labels = {
--             version = "v1",
--         }
--     }
-- }

local spec = obj.data.spec
local canary = {}
canary.labels = {}
canary.name = "canary"
for k, v in pairs(obj.patchPodMetadata.labels) do
    canary.labels[k] = v
    
end
table.insert(spec.subsets, canary)
return obj.data