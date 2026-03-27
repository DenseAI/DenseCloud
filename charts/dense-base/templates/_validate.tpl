{{- define "dense-base.validate" -}}
{{- $v := .values -}}
{{- $grpcIngress := default (dict) $v.grpc.ingress -}}
{{- $ingressHosts := default (list) $v.ingress.hosts -}}
{{- $grpcIngressHosts := default (list) $grpcIngress.hosts -}}
{{- $ingressTLS := default (list) $v.ingress.tls -}}
{{- $grpcIngressTLS := default (list) $grpcIngress.tls -}}
{{- $networkPolicy := default (dict) $v.networkPolicy -}}
{{- if not $v.image.repository -}}
{{- fail "dense-base: image.repository is required" -}}
{{- end -}}
{{- if and (not $v.grpc.enabled) $v.grpc.service.enabled -}}
{{- fail "dense-base: grpc.service.enabled requires grpc.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.enabled) (not $v.grpc.enabled) -}}
{{- fail "dense-base: grpc.tls.enabled requires grpc.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.enabled) (not $v.grpc.tls.certSecret) -}}
{{- fail "dense-base: grpc.tls.certSecret is required when grpc.tls.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.requireClientCert) (not $v.grpc.tls.clientCASecret) -}}
{{- fail "dense-base: grpc.tls.clientCASecret is required when grpc.tls.requireClientCert=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.certManager.enabled) (not $v.grpc.enabled) -}}
{{- fail "dense-base: grpc.tls.certManager.enabled requires grpc.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.certManager.enabled) (not $v.grpc.tls.enabled) -}}
{{- fail "dense-base: grpc.tls.certManager.enabled requires grpc.tls.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.certManager.enabled) (not $v.grpc.tls.certSecret) -}}
{{- fail "dense-base: grpc.tls.certSecret is required when grpc.tls.certManager.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.certManager.enabled) (not $v.grpc.tls.certManager.issuerRef.name) -}}
{{- fail "dense-base: grpc.tls.certManager.issuerRef.name is required when grpc.tls.certManager.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.grpc.tls.certManager.enabled) (eq (len (default (list) $v.grpc.tls.certManager.dnsNames)) 0) (not $v.grpc.tls.certManager.commonName) -}}
{{- fail "dense-base: grpc.tls.certManager requires at least one dnsName or commonName" -}}
{{- end -}}
{{- if and $v.ingress.enabled (not $v.service.enabled) -}}
{{- fail "dense-base: ingress.enabled requires service.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.ingress.certManager.enabled) (not $v.ingress.enabled) -}}
{{- fail "dense-base: ingress.certManager.enabled requires ingress.enabled=true" -}}
{{- end -}}
{{- if and (default false $v.ingress.certManager.enabled) (eq (len $ingressTLS) 0) -}}
{{- fail "dense-base: ingress.certManager.enabled requires at least one ingress.tls entry" -}}
{{- end -}}
{{- if and (default false $v.ingress.certManager.enabled) $v.ingress.certManager.clusterIssuer $v.ingress.certManager.issuer -}}
{{- fail "dense-base: ingress.certManager.clusterIssuer and issuer are mutually exclusive" -}}
{{- end -}}
{{- if and (default false $v.ingress.certManager.enabled) (not (or $v.ingress.certManager.clusterIssuer $v.ingress.certManager.issuer)) -}}
{{- fail "dense-base: ingress.certManager requires clusterIssuer or issuer" -}}
{{- end -}}
{{- if and $v.ingress.enabled (eq (len $ingressHosts) 0) -}}
{{- fail "dense-base: ingress.hosts must include at least one host when ingress.enabled=true" -}}
{{- end -}}
{{- range $tlsIdx, $tlsRule := $ingressTLS -}}
{{- if not $tlsRule.secretName -}}
{{- fail (printf "dense-base: ingress.tls[%d].secretName is required" $tlsIdx) -}}
{{- end -}}
{{- end -}}
{{- range $hostIdx, $hostRule := $ingressHosts -}}
{{- if not $hostRule.host -}}
{{- fail (printf "dense-base: ingress.hosts[%d].host is required" $hostIdx) -}}
{{- end -}}
{{- $paths := default (list) $hostRule.paths -}}
{{- if eq (len $paths) 0 -}}
{{- fail (printf "dense-base: ingress.hosts[%d].paths must include at least one path" $hostIdx) -}}
{{- end -}}
{{- range $pathIdx, $pathRule := $paths -}}
{{- if not $pathRule.path -}}
{{- fail (printf "dense-base: ingress.hosts[%d].paths[%d].path is required" $hostIdx $pathIdx) -}}
{{- end -}}
{{- if not $pathRule.pathType -}}
{{- fail (printf "dense-base: ingress.hosts[%d].paths[%d].pathType is required" $hostIdx $pathIdx) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if and (default false $grpcIngress.enabled) (or (not $v.grpc.enabled) (not $v.grpc.service.enabled)) -}}
{{- fail "dense-base: grpc.ingress.enabled requires grpc.enabled=true and grpc.service.enabled=true" -}}
{{- end -}}
{{- if and (default false $grpcIngress.certManager.enabled) (not (default false $grpcIngress.enabled)) -}}
{{- fail "dense-base: grpc.ingress.certManager.enabled requires grpc.ingress.enabled=true" -}}
{{- end -}}
{{- if and (default false $grpcIngress.certManager.enabled) (eq (len $grpcIngressTLS) 0) -}}
{{- fail "dense-base: grpc.ingress.certManager.enabled requires at least one grpc.ingress.tls entry" -}}
{{- end -}}
{{- if and (default false $grpcIngress.certManager.enabled) $grpcIngress.certManager.clusterIssuer $grpcIngress.certManager.issuer -}}
{{- fail "dense-base: grpc.ingress.certManager.clusterIssuer and issuer are mutually exclusive" -}}
{{- end -}}
{{- if and (default false $grpcIngress.certManager.enabled) (not (or $grpcIngress.certManager.clusterIssuer $grpcIngress.certManager.issuer)) -}}
{{- fail "dense-base: grpc.ingress.certManager requires clusterIssuer or issuer" -}}
{{- end -}}
{{- if and (default false $grpcIngress.enabled) (eq (len $grpcIngressHosts) 0) -}}
{{- fail "dense-base: grpc.ingress.hosts must include at least one host when grpc.ingress.enabled=true" -}}
{{- end -}}
{{- range $tlsIdx, $tlsRule := $grpcIngressTLS -}}
{{- if not $tlsRule.secretName -}}
{{- fail (printf "dense-base: grpc.ingress.tls[%d].secretName is required" $tlsIdx) -}}
{{- end -}}
{{- end -}}
{{- range $hostIdx, $hostRule := $grpcIngressHosts -}}
{{- if not $hostRule.host -}}
{{- fail (printf "dense-base: grpc.ingress.hosts[%d].host is required" $hostIdx) -}}
{{- end -}}
{{- $paths := default (list) $hostRule.paths -}}
{{- if eq (len $paths) 0 -}}
{{- fail (printf "dense-base: grpc.ingress.hosts[%d].paths must include at least one path" $hostIdx) -}}
{{- end -}}
{{- range $pathIdx, $pathRule := $paths -}}
{{- if not $pathRule.path -}}
{{- fail (printf "dense-base: grpc.ingress.hosts[%d].paths[%d].path is required" $hostIdx $pathIdx) -}}
{{- end -}}
{{- if not $pathRule.pathType -}}
{{- fail (printf "dense-base: grpc.ingress.hosts[%d].paths[%d].pathType is required" $hostIdx $pathIdx) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if and $v.serviceMonitor.enabled (not $v.service.enabled) -}}
{{- fail "dense-base: serviceMonitor.enabled requires service.enabled=true" -}}
{{- end -}}
{{- if and $v.serviceMonitor.enabled (not $v.serviceMonitor.path) -}}
{{- fail "dense-base: serviceMonitor.path is required when serviceMonitor.enabled=true" -}}
{{- end -}}
{{- if and $networkPolicy.enabled (not (or $networkPolicy.ingress.enabled $networkPolicy.egress.enabled)) -}}
{{- fail "dense-base: networkPolicy.enabled requires ingress.enabled or egress.enabled" -}}
{{- end -}}
{{- if not (has $v.model.source (list "none" "pvc" "emptyDir" "hostPath")) -}}
{{- fail "dense-base: model.source must be one of none,pvc,emptyDir,hostPath" -}}
{{- end -}}
{{- if and (eq $v.model.source "hostPath") (not $v.model.hostPath) -}}
{{- fail "dense-base: model.hostPath is required when model.source=hostPath" -}}
{{- end -}}
{{- if and (eq $v.model.source "pvc") (not $v.model.existingClaim) (not $v.model.pvc.size) -}}
{{- fail "dense-base: model.pvc.size is required when model.source=pvc and model.existingClaim is empty" -}}
{{- end -}}
{{- if and $v.keda.enabled (gt (int $v.keda.minReplicaCount) (int $v.keda.maxReplicaCount)) -}}
{{- fail "dense-base: keda.minReplicaCount cannot be greater than keda.maxReplicaCount" -}}
{{- end -}}
{{- if and $v.keda.enabled (eq (len (default (list) $v.keda.triggers.custom)) 0) -}}
{{- fail "dense-base: keda.triggers.custom must include at least one trigger when keda.enabled=true" -}}
{{- end -}}
{{- $pdbMin := $v.podDisruptionBudget.minAvailable -}}
{{- $pdbMax := $v.podDisruptionBudget.maxUnavailable -}}
{{- $pdbMinSet := not (or (eq $pdbMin nil) (eq (trim (toString $pdbMin)) "")) -}}
{{- $pdbMaxSet := not (or (eq $pdbMax nil) (eq (trim (toString $pdbMax)) "")) -}}
{{- if and $v.podDisruptionBudget.enabled $pdbMinSet $pdbMaxSet -}}
{{- fail "dense-base: podDisruptionBudget.minAvailable and maxUnavailable are mutually exclusive" -}}
{{- end -}}
{{- if and $v.podDisruptionBudget.enabled (not (or $pdbMinSet $pdbMaxSet)) -}}
{{- fail "dense-base: podDisruptionBudget requires either minAvailable or maxUnavailable" -}}
{{- end -}}
{{- end -}}
