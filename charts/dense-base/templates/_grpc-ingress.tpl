{{- define "dense-base.grpcIngress" -}}
{{- $v := .values -}}
{{- $grpcIngress := default (dict) $v.grpc.ingress -}}
{{- if and $v.grpc.enabled $v.grpc.service.enabled (default false $grpcIngress.enabled) -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "dense-base.fullname" . }}-grpc
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
  {{- if or $grpcIngress.annotations (default false $grpcIngress.certManager.enabled) }}
  annotations:
    {{- with $grpcIngress.annotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
    {{- if $grpcIngress.certManager.enabled }}
    {{- if $grpcIngress.certManager.clusterIssuer }}
    cert-manager.io/cluster-issuer: {{ $grpcIngress.certManager.clusterIssuer | quote }}
    {{- else if $grpcIngress.certManager.issuer }}
    cert-manager.io/issuer: {{ $grpcIngress.certManager.issuer | quote }}
    {{- end }}
    {{- with $grpcIngress.certManager.extraAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
    {{- end }}
  {{- end }}
spec:
  {{- if $grpcIngress.className }}
  ingressClassName: {{ $grpcIngress.className }}
  {{- end }}
  {{- if $grpcIngress.tls }}
  tls:
    {{- range $grpcIngress.tls }}
    - hosts:
      {{- range .hosts }}
        - {{ . | quote }}
      {{- end }}
      secretName: {{ .secretName }}
    {{- end }}
  {{- end }}
  rules:
    {{- range $grpcIngress.hosts }}
    - host: {{ .host | quote }}
      http:
        paths:
          {{- range .paths }}
          - path: {{ .path }}
            pathType: {{ .pathType }}
            backend:
              service:
                name: {{ include "dense-base.fullname" $ }}-grpc
                port:
                  number: {{ $v.grpc.service.port }}
          {{- end }}
    {{- end }}
{{- end -}}
{{- end -}}
