{{- define "dense-base.name" -}}
{{- $root := .root -}}
{{- $v := .values -}}
{{- default $root.Chart.Name $v.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "dense-base.fullname" -}}
{{- $root := .root -}}
{{- $v := .values -}}
{{- if $v.fullnameOverride -}}
{{- $v.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "dense-base.name" . -}}
{{- if contains $name $root.Release.Name -}}
{{- $root.Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" $root.Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "dense-base.labels" -}}
{{- $root := .root -}}
helm.sh/chart: {{ $root.Chart.Name }}-{{ $root.Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "dense-base.name" . }}
app.kubernetes.io/instance: {{ $root.Release.Name }}
app.kubernetes.io/version: {{ $root.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ $root.Release.Service }}
{{- end -}}

{{- define "dense-base.selectorLabels" -}}
{{- $root := .root -}}
app.kubernetes.io/name: {{ include "dense-base.name" . }}
app.kubernetes.io/instance: {{ $root.Release.Name }}
{{- end -}}

{{- define "dense-base.serviceAccountName" -}}
{{- $v := .values -}}
{{- if $v.serviceAccount.create -}}
{{- default (include "dense-base.fullname" .) $v.serviceAccount.name -}}
{{- else -}}
{{- default "default" $v.serviceAccount.name -}}
{{- end -}}
{{- end -}}
