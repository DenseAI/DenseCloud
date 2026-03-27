{{- define "dense-base.pvc" -}}
{{- $v := .values -}}
{{- if and (eq $v.model.source "pvc") (not $v.model.existingClaim) }}
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ include "dense-base.fullname" . }}-models
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
spec:
  {{- if $v.model.pvc.storageClassName }}
  storageClassName: {{ $v.model.pvc.storageClassName | quote }}
  {{- end }}
  accessModes:
    {{- toYaml $v.model.pvc.accessModes | nindent 4 }}
  resources:
    requests:
      storage: {{ $v.model.pvc.size | quote }}
{{- end }}
{{- end -}}
