-- obj = {
--     matches = {
--         {
--             headers = {
--                 {
--                     name = "xxx",
--                     value = "xxx",
--                     type = "RegularExpression"
--                 }
--             }
--         }
--     },
--     spec = {
-- 		hosts = {
-- 			"reviews",
-- 		},
--         http = {
--             {
--                 route = {
--                     {
--                         destination = {
--                             host = "reviews",
--                             subset = "c1"
--                         }
--                     }
--                 }
--             }
--         }
--     },
--     stableService = "reviews",
--     -- canaryService = "canary",
--     -- stableWeight = 90,
--     -- canaryWeight = 10
-- }


spec = obj.spec
if (obj.matches) then
    for _, match in ipairs(obj.matches) do
        local route = {}
        route["matches"] = {}
       
        for key, value in pairs(match) do
            local vsMatch = {}
            vsMatch[key] = {}
            for _, rule in ipairs(value) do
                if rule["type"] == "RegularExpression"
                then
                    matchType = "regex"
                else
                    matchType = "exact"
                end
                vsMatch[key][rule["name"]] = {}
                vsMatch[key][rule["name"]][matchType] = rule["value"]
            end
            table.insert(route["matches"], vsMatch)
        end
        route["route"] = {
            {
                destination = {
                    host = obj.stableService,
                    weight = obj.stableWeight
                }
            },
            {
                destination = {
                    host = obj.canaryService,
                    weight = obj.canaryWeight
                }
            }
        }
        table.insert(spec.http, 1, route)
    end
    return spec
end

for i, rule in ipairs(obj.spec.http) do
    for _, route in ipairs(rule.route) do
        local destination = route.destination
        if destination.host == obj.stableService then
            destination.weight = obj.stableWeight
            local canary = {
                destination = {
                    host = obj.canaryService,
                    weight = obj.canaryWeight
                }
            }
            table.insert(spec.http[i].route, canary)
        end
    end
end
return spec