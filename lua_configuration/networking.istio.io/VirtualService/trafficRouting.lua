spec = obj.data.spec

if obj.canaryWeight == -1 then
    obj.canaryWeight = 100
    obj.stableWeight = 0
end

function GetHost(destination)
    local host = destination.destination.host
    dot_position = string.find(host, ".", 1, true)
    if (dot_position) then
        host = string.sub(host, 1, dot_position - 1)
    end
    return host
end

-- find routes of VirtualService with stableService
function GetRulesToPatch(spec, stableService, protocol)
    local matchedRoutes = {}
    if (spec[protocol] ~= nil) then
        for _, rule in ipairs(spec[protocol]) do
            -- skip routes contain matches
            if (rule.match == nil) then
                for _, route in ipairs(rule.route) do
                    if GetHost(route) == stableService then
                        table.insert(matchedRoutes, rule)
                    end
                end
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

-- generate routes with matches, insert a rule before other rules, only support http headers, cookies etc.
function GenerateRoutesWithMatches(spec, matches, stableService, canaryService, requestHeaderModifier)
    for _, match in ipairs(matches) do
        local route = {}
        route["match"] = {}

        local vsMatch = {}
        for key, value in pairs(match) do
            if key == "path" then
                vsMatch["uri"] = {}
                local rule = value
                if rule["type"] == "RegularExpression" then
                    matchType = "regex"
                elseif rule["type"] == "Exact" then
                    matchType = "exact"
                elseif rule["type"] == "PathPrefix" then
                    matchType = "prefix"
                end
                vsMatch["uri"][matchType] = rule.value
            else
                vsMatch[key] = {}
                for _, rule in ipairs(value) do
                    if rule["type"] == "RegularExpression" then
                        matchType = "regex"
                    elseif rule["type"] == "Exact" then
                        matchType = "exact"
                    elseif rule["type"] == "Prefix" then
                        matchType = "prefix"
                    end
                    if key == "headers" or key == "queryParams" then
                        vsMatch[key][rule["name"]] = {}
                        vsMatch[key][rule["name"]][matchType] = rule.value
                    else
                        vsMatch[key][matchType] = rule.value
                    end
                end
            end
        end
        table.insert(route["match"], vsMatch)
        if requestHeaderModifier then
            route["headers"] = {}
            route["headers"]["request"] = {}
            for action, headers in pairs(requestHeaderModifier) do
                if action == "set" or action == "add" then
                    route["headers"]["request"][action] = {}
                    for _, header in ipairs(headers) do
                        route["headers"]["request"][action][header["name"]] = header["value"]
                    end
                elseif action == "remove" then
                    route["headers"]["request"]["remove"] = {}
                    for _, rHeader in ipairs(headers) do
                        table.insert(route["headers"]["request"]["remove"], rHeader)
                    end
                end
            end
        end
        route.route = {
            {
                destination = {}
            }
        }
        -- stableService == canaryService indicates DestinationRule exists and subset is set to be canary by default
        if stableService == canaryService then
            route.route[1].destination.host = stableService
            route.route[1].destination.subset = "canary"
        else
            route.route[1].destination.host = canaryService
        end
        table.insert(spec.http, 1, route)
    end
end

-- generate routes without matches, change every rule whose host is stableService
function GenerateRoutes(spec, stableService, canaryService, stableWeight, canaryWeight, protocol)
    local matchedRules = GetRulesToPatch(spec, stableService, protocol)
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

        -- incase there are multiple versions traffic already, do a for-loop
        for _, route in ipairs(rule.route) do
            -- update stable service weight
            route.weight = CalculateWeight(route, stableWeight, #rule.route)
        end
        table.insert(rule.route, canary)
    end
end

if (obj.matches and next(obj.matches) ~= nil)
then
    GenerateRoutesWithMatches(spec, obj.matches, obj.stableService, obj.canaryService, obj.requestHeaderModifier)
else
    GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, "http")
    GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, "tcp")
    GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, "tls")
end
return obj.data
