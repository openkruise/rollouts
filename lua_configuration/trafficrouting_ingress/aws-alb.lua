local annotations = obj.annotations or {}

-- Remove any prior ALB canary annotations
annotations["alb.ingress.kubernetes.io/actions.canary"]    = nil
annotations["alb.ingress.kubernetes.io/conditions.canary"] = nil

-- Compute weights
local cw = tonumber(obj.weight) or 0
if cw < 0 then cw = 0 end
if cw > 100 then cw = 100 end
local sw = 100 - cw

-- Build the forward action with *both* target‐groups
local action = {
  Type = "forward",
  ForwardConfig = {
    TargetGroups = {
      { ServiceName = obj.stableService, ServicePort = "80", Weight = sw },
      { ServiceName = obj.canaryService, ServicePort = "80", Weight = cw },
    }
  }
}
annotations["alb.ingress.kubernetes.io/actions.canary"] = action

-- Build conditions if any
if obj.matches then
  local conds = {}
  for _, match in ipairs(obj.matches) do
    for _, hdr in ipairs(match.headers or {}) do
      if hdr.name == "canary-by-cookie" then
        table.insert(conds, {
          Field            = "http-cookie",
          HttpCookieConfig = { CookieName = hdr.value, Values = { hdr.value } },
        })
      elseif hdr.name == "SourceIp" then
        table.insert(conds, {
          Field          = "source-ip",
          SourceIpConfig = { Values = { hdr.value } },
        })
      else
        table.insert(conds, {
          Field = "http-header",
          HttpHeaderConfig = {
            HttpHeaderName = hdr.name,
            Values         = { hdr.value },
          },
        })
      end
    end
  end
  annotations["alb.ingress.kubernetes.io/conditions.canary"] = conds
end

return annotations
