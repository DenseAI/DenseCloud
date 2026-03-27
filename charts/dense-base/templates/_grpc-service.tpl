{{- define "dense-base.grpcService" -}}
{{- $v := .values -}}
{{- if and $v.grpc.enabled $v.grpc.service.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "dense-base.fullname" . }}-grpc
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
  {{- with $v.grpc.service.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  type: {{ $v.grpc.service.type }}
  selector:
    {{- include "dense-base.selectorLabels" . | nindent 4 }}
  ports:
    - name: grpc
      protocol: TCP
      port: {{ $v.grpc.service.port }}
      targetPort: grpc
    {{- with $v.grpc.service.extraPorts }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
{{- end }}
{{- end -}}
