steps={step_0={canaryWeight=-1,stableWeight=101,matches={{headers={{value="demo",type="Exact",name="destination",},},},},canaryService="mocka",stableService="mocka",patchPodMetadata={labels={version="canary",},},data={spec={subsets={{labels={version="base",},name="version-base",},},trafficPolicy={loadBalancer={simple="ROUND_ROBIN",},},host="svc-a",},},},}