{{- define "dense-base.service" -}}
{{- $v := .values -}}
{{- if $v.service.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
  {{- with $v.service.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  type: {{ $v.service.type }}
  selector:
    {{- include "dense-base.selectorLabels" . | nindent 4 }}
  ports:
    - name: http
      protocol: TCP
      port: {{ $v.service.port }}
      targetPort: {{ $v.service.targetPort }}
    {{- with $v.service.extraPorts }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
{{- end }}
{{- end -}}
