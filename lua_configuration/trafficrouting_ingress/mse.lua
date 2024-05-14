function split(input, delimiter)
    local arr = {}
    string.gsub(input, '[^' .. delimiter ..']+', function(w) table.insert(arr, w) end)
    return arr
end

annotations = obj.annotations
annotations["nginx.ingress.kubernetes.io/canary"] = "true"
annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = nil
annotations["nginx.ingress.kubernetes.io/canary-by-header"] = nil
annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = nil
annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = nil
-- MSE extended annotations
annotations["mse.ingress.kubernetes.io/canary-by-query"] = nil
annotations["mse.ingress.kubernetes.io/canary-by-query-pattern"] = nil
annotations["mse.ingress.kubernetes.io/canary-by-query-value"] = nil
annotations["nginx.ingress.kubernetes.io/canary-weight"] = nil
if ( obj.weight ~= "-1" )
then
    annotations["nginx.ingress.kubernetes.io/canary-weight"] = obj.weight
end
if ( annotations["mse.ingress.kubernetes.io/service-subset"] )
then
    annotations["mse.ingress.kubernetes.io/service-subset"] = "gray"
end

if ( obj.requestHeaderModifier )
then
    local str = ''
    for _,header in ipairs(obj.requestHeaderModifier.set) do
        str = str..string.format("%s %s", header.name, header.value)
    end
    annotations["mse.ingress.kubernetes.io/request-header-control-update"] = str
end
if ( not obj.matches )
then
    return annotations
end
for _,match in ipairs(obj.matches) do
    if match.headers and next(match.headers) ~= nil then
        header = match.headers[1]
        if ( header.name == "canary-by-cookie" )
        then
            annotations["nginx.ingress.kubernetes.io/canary-by-cookie"] = header.value
        else
            annotations["nginx.ingress.kubernetes.io/canary-by-header"] = header.name
            if ( header.type == "RegularExpression" )
            then
                annotations["nginx.ingress.kubernetes.io/canary-by-header-pattern"] = header.value
            else
                annotations["nginx.ingress.kubernetes.io/canary-by-header-value"] = header.value
            end
        end
    end
    if match.queryParams and next(match.queryParams) ~= nil then
        queryParam = match.queryParams[1]
        annotations["nginx.ingress.kubernetes.io/canary-by-query"] = queryParam.name
        if ( queryParam.type == "RegularExpression" )
        then
            annotations["nginx.ingress.kubernetes.io/canary-by-query-pattern"] = queryParam.value
        else
            annotations["nginx.ingress.kubernetes.io/canary-by-query-value"] = queryParam.value
        end
    end
end
return annotations