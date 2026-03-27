{{- define "dense-base.serviceMonitor" -}}
{{- $v := .values -}}
{{- if $v.serviceMonitor.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
    {{- with $v.serviceMonitor.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  selector:
    matchLabels:
      {{- include "dense-base.selectorLabels" . | nindent 6 }}
  endpoints:
    - port: http
      path: {{ $v.serviceMonitor.path }}
      interval: {{ $v.serviceMonitor.interval }}
      scrapeTimeout: {{ $v.serviceMonitor.scrapeTimeout }}
  namespaceSelector:
    matchNames:
      - {{ .root.Release.Namespace }}
{{- end }}
{{- end -}}
