{{- define "dense-base.grpcCertificate" -}}
{{- $v := .values -}}
{{- if and $v.grpc.enabled $v.grpc.tls.enabled (default false $v.grpc.tls.certManager.enabled) }}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ include "dense-base.fullname" . }}-grpc
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
spec:
  secretName: {{ $v.grpc.tls.certSecret | quote }}
  issuerRef:
    name: {{ $v.grpc.tls.certManager.issuerRef.name | quote }}
    kind: {{ default "ClusterIssuer" $v.grpc.tls.certManager.issuerRef.kind | quote }}
    group: {{ default "cert-manager.io" $v.grpc.tls.certManager.issuerRef.group | quote }}
  {{- if $v.grpc.tls.certManager.commonName }}
  commonName: {{ $v.grpc.tls.certManager.commonName | quote }}
  {{- end }}
  {{- with $v.grpc.tls.certManager.dnsNames }}
  dnsNames:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- if $v.grpc.tls.certManager.duration }}
  duration: {{ $v.grpc.tls.certManager.duration | quote }}
  {{- end }}
  {{- if $v.grpc.tls.certManager.renewBefore }}
  renewBefore: {{ $v.grpc.tls.certManager.renewBefore | quote }}
  {{- end }}
  {{- with $v.grpc.tls.certManager.usages }}
  usages:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- end -}}
