{{- define "dense-base.serviceAccount" -}}
{{- $v := .values -}}
{{- if $v.serviceAccount.create -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "dense-base.serviceAccountName" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
  {{- with $v.serviceAccount.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
automountServiceAccountToken: {{ $v.serviceAccount.automount }}
{{- end -}}
{{- end -}}
