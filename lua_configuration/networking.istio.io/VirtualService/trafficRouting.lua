-- obj = {
--     matches = {
--         {
--             uri = {
--                 {
--                     name = "xxx",
--                     value = "xxx",
--                     type = "Prefix"
--                 }
--             }
--         }
--     },
--     data = {
--         spec = {
--             hosts = {
--                 "reviews",
--             },
--             http = {
--                 {
--                     route = {
--                         {
--                             destination = {
--                                 host = "reviews",
--                                 subset = "c1",
--                                 port = {
--                                     number = 80
--                                 }
--                             }
--                         }
--                     }
--                 }
--             }
--         },
--     },
    
--     stableService = "reviews",
--     canaryService = "canary",
--     stableWeight = 90,
--     canaryWeight = 10
-- }

spec = obj.data.spec

-- find matched route of VirtualService spec with stable svc
function FindMatchedRules(spec, stableService)
    local matchedRoutes = {}
    local rules = {}
    if (spec.http) then
        for _, http in ipairs(spec.http) do
            table.insert(rules, http)
        end
    end
    if (spec.tls) then
        for _, tls in ipairs(spec.tls) do
            table.insert(rules, tls)
        end
    end
    if (spec.tcp) then
        for _, tcp in ipairs(spec.tcp) do
            table.insert(rules, tcp)
        end
    end
    for _, rule in ipairs(rules) do
        for _, route in ipairs(rule.route) do
            if route.destination.host == stableService then
                table.insert(matchedRoutes, rule)
            end
        end
    end
    return matchedRoutes
end

-- generate routes with matches
function GenerateMatchedRoutes(spec, matches, stableService, canaryService, stableWeight, canaryWeight)
    local route = {}
    route["match"] = {}
    for _, match in ipairs(matches) do
        for key, value in pairs(match) do
            local vsMatch = {}
            vsMatch[key] = {}
            for _, rule in ipairs(value) do
                if rule["type"] == "RegularExpression" then
                    matchType = "regex"
                elseif rule["type"] == "Exact" then
                    matchType = "exact"
                elseif rule["type"] == "Prefix" then
                    matchType = "prefix"
                end
                if key == "headers" then
                    vsMatch[key][rule["name"]] = {}
                    vsMatch[key][rule["name"]][matchType] = rule.value
                else
                    vsMatch[key][matchType] = rule.value
                end
            end
            table.insert(route["match"], vsMatch)
        end
    end
    route["route"] = {
        {
            destination = {
                host = stableService,
            },
            weight = stableWeight,
        },
        {
            destination = {
                host = canaryService,
            },
            weight = canaryWeight,
        }
    }
    table.insert(spec.http, 1, route)
end

-- generate routes without matches
function GenerateRoutes(spec, stableService, canaryService, stableWeight, canaryWeight)
    local matchedRules = FindMatchedRules(spec, stableService)
    for _, rule in ipairs(matchedRules) do
        local canary = {
            destination = {
                host = canaryService,
            },
            weight = canaryWeight,
        }
        for _, route in ipairs(rule.route) do
            -- incase there are multiple versions traffic already
            if (route.weight) then
                route.weight = math.floor(route.weight * stableWeight / 100)
            else
                route.weight = math.floor(stableWeight / #rule.route)
            end
        end
        table.insert(rule.route, canary)
    end
end

if (obj.matches) then
    GenerateMatchedRoutes(spec, obj.matches, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight)
    return obj.data
end

GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight)
return obj.data

