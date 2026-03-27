{{- define "dense-base.keda" -}}
{{- $v := .values -}}
{{- if $v.keda.enabled }}
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
    {{- with $v.keda.triggers.custom }}
    {{- toYaml . | nindent 4 }}
    {{- else }}
    - type: prometheus
      metadata:
        serverAddress: {{ $v.keda.prometheusServerAddress | quote }}
        metricName: "pending_requests"
        threshold: "1"
        query: "vector(0)"
    {{- end }}
{{- end }}
{{- end -}}
