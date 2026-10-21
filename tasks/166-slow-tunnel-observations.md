# 166. Slow tunnel observations

Implement with issue 218 and ADR 0031. A successful twelve-second round trip must
report slow within the fifteen-second budget. Timeout and generic probe failure
remain indeterminate transport evidence. Manual and scheduled checks share bounded
capacity that cannot be released early by caller cancellation.
