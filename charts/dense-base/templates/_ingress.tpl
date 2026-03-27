{{- define "dense-base.ingress" -}}
{{- $v := .values -}}
{{- if $v.ingress.enabled -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
  {{- if or $v.ingress.annotations (default false $v.ingress.certManager.enabled) }}
  annotations:
    {{- with $v.ingress.annotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
    {{- if $v.ingress.certManager.enabled }}
    {{- if $v.ingress.certManager.clusterIssuer }}
    cert-manager.io/cluster-issuer: {{ $v.ingress.certManager.clusterIssuer | quote }}
    {{- else if $v.ingress.certManager.issuer }}
    cert-manager.io/issuer: {{ $v.ingress.certManager.issuer | quote }}
    {{- end }}
    {{- with $v.ingress.certManager.extraAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
    {{- end }}
  {{- end }}
spec:
  {{- if $v.ingress.className }}
  ingressClassName: {{ $v.ingress.className }}
  {{- end }}
  {{- if $v.ingress.tls }}
  tls:
    {{- range $v.ingress.tls }}
    - hosts:
      {{- range .hosts }}
        - {{ . | quote }}
      {{- end }}
      secretName: {{ .secretName }}
    {{- end }}
  {{- end }}
  rules:
    {{- range $v.ingress.hosts }}
    - host: {{ .host | quote }}
      http:
        paths:
          {{- range .paths }}
          - path: {{ .path }}
            pathType: {{ .pathType }}
            backend:
              service:
                name: {{ include "dense-base.fullname" $ }}
                port:
                  number: {{ $v.service.port }}
          {{- end }}
    {{- end }}
{{- end -}}
{{- end -}}
