{{- define "dense-base.keda" -}}
{{- $v := .values -}}
{{- if $v.keda.enabled }}
{{- if eq (len (default (list) $v.keda.triggers.custom)) 0 -}}
{{- fail "dense-base: keda.triggers.custom must include at least one trigger when keda.enabled=true" -}}
{{- end -}}
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ include "dense-base.fullname" . }}
  minReplicaCount: {{ $v.keda.minReplicaCount }}
  maxReplicaCount: {{ $v.keda.maxReplicaCount }}
  pollingInterval: {{ $v.keda.pollingInterval }}
  cooldownPeriod: {{ $v.keda.cooldownPeriod }}
  {{- with $v.keda.advanced }}
  advanced:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  triggers:
    {{- toYaml $v.keda.triggers.custom | nindent 4 }}
{{- end }}
{{- end -}}
