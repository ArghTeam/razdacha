# Restore authentication and automatic pool safety

Issue #326: audit F01, F03, F04; umbrella #324.

Before publishing a staged restore, atomically delete every panel session and rotate authentication generation while preserving the restored password and WireGuard keys. Keep existing REALITY reset and guarded explicit Apply. Guidance must distinguish reusable WireGuard profiles from mandatory REALITY reconfiguration, profile reissue and Apply; endpoint changes are independent.

Automatic pool admission and stored-member generation reject Shadowsocks plaintext none and malformed or unknown cipher declarations. Known encrypted methods remain eligible; manual import and country filtering remain unchanged.

Verify synthetic SQLite restore only: saved live/revoked/expired sessions, stale generation, fresh login issuance, rollback, preserved keys and guidance. Verify plain/base64/malformed pool links, encrypted members and manual compatibility.

Primary `/Users/romanzhigalkin/Web/razdacha`, origin/main freshly fetched at `8dc7cdbe50d6b6639d06cabfc7890fa75d7172cf`. Feature `.codex/worktrees/fix-326-restore-pool-safety`, branch `fix/326-restore-pool-safety`.
