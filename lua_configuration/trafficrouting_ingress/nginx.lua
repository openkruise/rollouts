annotations = {}
-- obj.annotations is ingress annotations, it is recommended not to remove the part of the lua script, it must be kept
if ( obj.annotations )
then
    annotations = obj.annotations
end
-- indicates the ingress is nginx canary api
annotations["nginx.ingress.kubernetes.io/canary"] = "true"
-- First, set all canary api to nil
annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = nil
annotations["nginx.ingress.kubernetes.io/canary-by-header"] = nil
annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = nil
annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = nil
annotations["nginx.ingress.kubernetes.io/canary-weight"] = nil
-- if rollout.spec.strategy.canary.steps.weight is nil, obj.weight will be -1,
-- then we need remove the canary-weight annotation
if ( obj.weight ~= "-1" )
then
    annotations["nginx.ingress.kubernetes.io/canary-weight"] = obj.weight
end
-- if don't contains headers, immediate return annotations
if ( not obj.matches )
then
    return annotations
end
-- headers & cookie apis
-- traverse matches
for _,match in ipairs(obj.matches) do
    if match.headers and next(match.headers) ~= nil then
        local header = match.headers[1]
        -- cookie
        if ( header.name == "canary-by-cookie" )
        then
            annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = header.value
        else
            annotations["nginx.ingress.kubernetes.io/canary-by-header"] = header.name
            -- if regular expression
            if ( header.type == "RegularExpression" )
            then
                annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = header.value
            else
                annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = header.value
            end
        end
    end
end
-- must be return annotations
return annotations
