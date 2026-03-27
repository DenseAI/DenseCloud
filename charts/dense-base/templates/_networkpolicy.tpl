{{- define "dense-base.networkPolicy" -}}
{{- $v := .values -}}
{{- $np := default (dict) $v.networkPolicy -}}
{{- $ingressPeers := default (list) $np.ingress.peers -}}
{{- $ingressPorts := default (list) $np.ingress.ports -}}
{{- $egressPeers := default (list) $np.egress.peers -}}
{{- $egressPorts := default (list) $np.egress.ports -}}
{{- if $np.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      {{- include "dense-base.selectorLabels" . | nindent 6 }}
  policyTypes:
    {{- if $np.ingress.enabled }}
    - Ingress
    {{- end }}
    {{- if $np.egress.enabled }}
    - Egress
    {{- end }}
  {{- if $np.ingress.enabled }}
  {{- if or $np.ingress.allowAll $np.ingress.allowSameNamespace (gt (len $ingressPeers) 0) }}
  ingress:
    {{- if and $np.ingress.allowAll (eq (len $ingressPorts) 0) }}
    - {}
    {{- else }}
    - {{- if not $np.ingress.allowAll }}
      from:
        {{- if $np.ingress.allowSameNamespace }}
        - podSelector: {}
        {{- end }}
        {{- with $ingressPeers }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- end }}
      {{- if gt (len $ingressPorts) 0 }}
      ports:
        {{- range $ingressPorts }}
        - protocol: {{ default "TCP" .protocol }}
          port: {{ .port }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
  {{- end }}
  {{- if $np.egress.enabled }}
  {{- if or $np.egress.allowDNS $np.egress.allowAll (gt (len $egressPeers) 0) (gt (len $egressPorts) 0) }}
  egress:
    {{- if $np.egress.allowDNS }}
    - ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    {{- end }}
    {{- if or $np.egress.allowAll (gt (len $egressPeers) 0) (gt (len $egressPorts) 0) }}
    {{- if and $np.egress.allowAll (eq (len $egressPorts) 0) }}
    - {}
    {{- else }}
    - {{- if and (not $np.egress.allowAll) (gt (len $egressPeers) 0) }}
      to:
        {{- toYaml $egressPeers | nindent 8 }}
      {{- end }}
      {{- if gt (len $egressPorts) 0 }}
      ports:
        {{- range $egressPorts }}
        - protocol: {{ default "TCP" .protocol }}
          port: {{ .port }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- end }}
  {{- end }}
  {{- end }}
{{- end }}
{{- end -}}
