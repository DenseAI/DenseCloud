{{- define "dense-base.pdb" -}}
{{- $v := .values -}}
{{- $pdbMin := $v.podDisruptionBudget.minAvailable -}}
{{- $pdbMax := $v.podDisruptionBudget.maxUnavailable -}}
{{- $pdbMinSet := not (or (eq $pdbMin nil) (eq (trim (toString $pdbMin)) "")) -}}
{{- $pdbMaxSet := not (or (eq $pdbMax nil) (eq (trim (toString $pdbMax)) "")) -}}
{{- if $v.podDisruptionBudget.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
spec:
  {{- if $pdbMinSet }}
  minAvailable: {{ $pdbMin }}
  {{- else if $pdbMaxSet }}
  maxUnavailable: {{ $pdbMax }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "dense-base.selectorLabels" . | nindent 6 }}
{{- end }}
{{- end -}}
