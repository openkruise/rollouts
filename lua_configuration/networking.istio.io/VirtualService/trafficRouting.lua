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

-- AN VIRUTALSERVICE EMXAPLE
-- apiVersion: networking.istio.io/v1alpha3
-- kind: VirtualService
-- metadata:
--   name: productpage
--   namespace: nsA
-- spec:
--   http:
--   - match:
--      - uri:
--         prefix: "/productpage/v1/"
--     route:
--     - destination:
--         host: productpage-v1.nsA.svc.cluster.local
--   - route:
--     - destination:
--         host: productpage.nsA.svc.cluster.local

function DeepCopy(original)
    local copy
    if type(original) == 'table' then
        copy = {}
        for key, value in pairs(original) do
            copy[key] = DeepCopy(value)
        end
    else
        copy = original
    end
    return copy
end

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

function FindMatchedDestination(spec, stableService)
    local matchedDst = {}
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
                matchedDst = route.destination
                return matchedDst
            end
        end
    end
    return matchedDst
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
                if rule["type"] == "RegularExpression"
                then
                    matchType = "regex"
                else
                    matchType = "exact"
                end
                vsMatch[key][rule["name"]] = {}
                vsMatch[key][rule["name"]][matchType] = rule["value"]
            end
            table.insert(route["match"], vsMatch)
        end
    end
    local matchedDst = FindMatchedDestination(spec, stableService)
    route["route"] = {
        {
            destination = DeepCopy(matchedDst),
            weight = stableWeight,
        },
        {
            destination = {
                host = canaryService,
                port = DeepCopy(matchedDst.port)
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
            if (route.destination.host == stableService) then
                canary.destination.port = DeepCopy(route.destination.port)
            end
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

