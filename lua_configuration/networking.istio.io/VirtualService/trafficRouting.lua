spec = obj.data.spec

if obj.canaryWeight == -1 then
    obj.canaryWeight = 100
    obj.stableWeight = 0
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
                break
            end
        end
    end
    return matchedRoutes
end

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

-- find routes that route traffic to stable service
function FindMatchedRoutes(spec, stableService)
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
                table.insert(matchedRoutes, route)
            end
        end
    end
    return matchedRoutes
end

function CalculateWeight(route, stableWeight, n)
    local weight
    if (route.weight) then
        weight = math.floor(route.weight * stableWeight / 100)
    else
        weight = math.floor(stableWeight / n)
    end
    return weight
end

-- generate routes with matches
function GenerateMatchedRoutes(spec, matches, stableService, canaryService, stableWeight, canaryWeight)
    for _, match in ipairs(matches) do
        local route = {}
        route["match"] = {}

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
        route.route = {
            {
                destination = {}
            }
        }
        if stableWeight ~= 0 then
            local matchedRoutes = FindMatchedRoutes(spec, stableService)
            -- update every matched route
            for _, r in ipairs(matchedRoutes) do
                local nRoute = DeepCopy(r)
                nRoute.weight = CalculateWeight(nRoute, stableWeight, #matchedRoutes)
                table.insert(route.route, nRoute)
            end
            route.route[1].weight = canaryWeight
        end
        -- if stableService == canaryService, then do e2e release
        if stableService == canaryService then
            route.route[1].destination.host = stableService
            route.route[1].destination.subset = "canary"
        else
            route.route[1].destination.host = canaryService
        end
        table.insert(spec.http, 1, route)
    end
end

-- generate routes without matches
function GenerateRoutes(spec, stableService, canaryService, stableWeight, canaryWeight)
    local matchedRules = FindMatchedRules(spec, stableService)
    for _, rule in ipairs(matchedRules) do
        local canary
        if stableService ~= canaryService then
            canary = {
                destination = {
                    host = canaryService,
                },
                weight = canaryWeight,
            }
        else
            canary = {
                destination = {
                    host = stableService,
                    subset = "canary",
                },
                weight = canaryWeight,
            }
        end
        
        for _, route in ipairs(rule.route) do
            -- incase there are multiple versions traffic already
            route.weight = CalculateWeight(route, stableWeight, #rule.route)
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