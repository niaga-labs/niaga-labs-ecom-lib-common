# Lessons

## 2026-10-02 — proxy identity is a chain of trust boundaries (NIAGA-597)

- Believed: nginx appending the caller's XFF and Gin's defaults would identify the actual client.
- True: Gin trusts every proxy by default, so a forged header can select another rate-limit bucket.
- Root cause: proxy trust was not configured centrally and the public gateway kept untrusted input.
- Rule: configure engines before serving, overwrite headers at public ingress, and preserve validated
  BFF forwarding only on a private listener. Test the socket trust decision and real limiter together.
