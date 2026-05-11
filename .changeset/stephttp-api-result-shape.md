---
"inngestgo": patch
---

Align `stephttp` `APIResult` wire format with the JS SDK: rename `status_code` to `status`, emit `body` as a string instead of base64-encoded bytes, and stop wrapping the run-complete payload in a `data` envelope.
