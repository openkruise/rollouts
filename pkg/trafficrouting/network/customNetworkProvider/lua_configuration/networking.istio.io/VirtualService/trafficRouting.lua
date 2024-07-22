spec = obj.data.spec

if obj.canaryWeight == -1 then
    obj.canaryWeight = 100
    obj.stableWeight = 0
end

function FindRules(spec, protocol)
    local rules = {}
    if (protocol == "http") then
        if (spec.http ~= nil) then
            for _, http in ipairs(spec.http) do
                table.insert(rules, http)
            end
        end
    elseif (protocol == "tcp") then
        if (spec.tcp ~= nil) then
            for _, http in ipairs(spec.tcp) do
                table.insert(rules, http)
            end
        end
    elseif (protocol == "tls") then
        if (spec.tls ~= nil) then
            for _, http in ipairs(spec.tls) do
                table.insert(rules, http)
            end
        end
    end
    return rules
end

-- find matched route of VirtualService spec with stable svc
function FindMatchedRules(spec, stableService, protocol)
    local matchedRoutes = {}
    local rules = FindRules(spec, protocol)
    -- a rule contains 'match' and 'route'
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

function FindStableServiceSubsets(spec, stableService, protocol)
    local stableSubsets = {}
    local rules = FindRules(spec, protocol)
    local hasRule = false
    -- a rule contains 'match' and 'route'
    for _, rule in ipairs(rules) do
        for _, route in ipairs(rule.route) do
            if route.destination.host == stableService then
                hasRule = true
                local contains = false
                for _, v in ipairs(stableSubsets) do
                    if v == route.destination.subset then
                        contains = true
                        break
                    end
                end
                if not contains and route.destination.subset ~= nil then
                    table.insert(stableSubsets, route.destination.subset)
                end
            end
        end
    end
    return hasRule, stableSubsets
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

function CalculateWeight(route, stableWeight, n)
    local weight
    if (route.weight) then
        weight = math.floor(route.weight * stableWeight / 100)
    else
        weight = math.floor(stableWeight / n)
    end
    return weight
end

-- generate routes with matches, insert a rule before other rules
function GenerateMatchedRoutes(spec, matches, stableService, canaryService, stableWeight, canaryWeight, requestHeaderModifier, protocol)
    local hasRule, stableServiceSubsets = FindStableServiceSubsets(spec, stableService, protocol)
    if (not hasRule) then
        return
    end
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
        -- if stableWeight != 0, then add stable service destinations
        -- incase there are multiple subsets in stable service
        if stableWeight ~= 0 then
            local nRoute = {}
            if #stableServiceSubsets ~= 0 then
                local weight = CalculateWeight(nRoute, stableWeight, #stableServiceSubsets)
                for _, r in ipairs(stableServiceSubsets) do
                    nRoute = {
                        destination = {
                            host = stableService,
                            subset = r
                        },
                        weight = weight
                    }
                    table.insert(route.route, nRoute)
                end
            else
                nRoute = {
                    destination = {
                        host = stableService
                    },
                    weight = stableWeight
                }
                table.insert(route.route, nRoute)
            end
            -- update every matched route
            route.route[1].weight = canaryWeight
        end
        -- if stableService == canaryService, then do e2e release
        if stableService == canaryService then
            route.route[1].destination.host = stableService
            route.route[1].destination.subset = "canary"
        else
            route.route[1].destination.host = canaryService
        end
        if (protocol == "http") then
            table.insert(spec.http, 1, route)
        elseif (protocol == "tls") then
            table.insert(spec.tls, 1, route)
        elseif (protocol == "tcp") then
            table.insert(spec.tcp, 1, route)
        end
    end
end

-- generate routes without matches, change every rule
function GenerateRoutes(spec, stableService, canaryService, stableWeight, canaryWeight, requestHeaderModifier, protocol)
    local matchedRules = FindMatchedRules(spec, stableService, protocol)
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

if (obj.matches) then
    GenerateMatchedRoutes(spec, obj.matches, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, obj.requestHeaderModifier, "http")
    GenerateMatchedRoutes(spec, obj.matches, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, obj.requestHeaderModifier,"tcp")
    GenerateMatchedRoutes(spec, obj.matches, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, obj.requestHeaderModifier,"tls")
else
    GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, obj.requestHeaderModifier, "http")
    GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, obj.requestHeaderModifier, "tcp")
    GenerateRoutes(spec, obj.stableService, obj.canaryService, obj.stableWeight, obj.canaryWeight, obj.requestHeaderModifier, "tls")
end
return obj.data
