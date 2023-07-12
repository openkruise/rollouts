annotations = {}
if ( obj.annotations )
then
    annotations = obj.annotations
end
annotations["alb.ingress.kubernetes.io/canary"] = "true"
annotations["alb.ingress.kubernetes.io/canary-by-cookie"] = nil
annotations["alb.ingress.kubernetes.io/canary-by-header"] = nil
annotations["alb.ingress.kubernetes.io/canary-by-header-pattern"] = nil
annotations["alb.ingress.kubernetes.io/canary-by-header-value"] = nil
annotations["alb.ingress.kubernetes.io/canary-weight"] = nil
annotations["alb.ingress.kubernetes.io/order"] = "1"
if ( obj.weight ~= "-1" )
then
    annotations["alb.ingress.kubernetes.io/canary-weight"] = obj.weight
end
if ( not obj.matches )
then
    return annotations
end
for _,match in ipairs(obj.matches) do
    local header = match.headers[1]
    if ( header.name == "canary-by-cookie" )
    then
        annotations["alb.ingress.kubernetes.io/canary-by-cookie"] = header.value
    else
        annotations["alb.ingress.kubernetes.io/canary-by-header"] = header.name
        if ( header.type == "RegularExpression" )
        then
            annotations["alb.ingress.kubernetes.io/canary-by-header-pattern"] = header.value
        else
            annotations["alb.ingress.kubernetes.io/canary-by-header-value"] = header.value
        end
    end
end
return annotations
