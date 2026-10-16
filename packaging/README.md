# Speakers 0.26.23 / Control 0.10.16 draft composition

This branch starts from `d2aaaa044d7fed43f2d192de9f6726e84332b6d2`, the merged Speakers 0.26.22 / Control 0.10.15 source. The reviewed manager and Control queue files are copied byte-for-byte from the isolated PodView queue candidate. The new Native and OwnTone patches are appended after the existing 22 patch stacks; older patch bytes are unchanged. Dockerfile and the verification workflow check patch hashes before applying them.

`room-queue-native.patch` is the combined 14-file Native delta, SHA-256 `29cabcb74969c8a2e605c3eec166141ad812732037fadd1a94d581ee0af63d72`. `room-queue-owntone.patch` is the combined 12-file C delta, SHA-256 `5015a42d0ca6cab0fc535944c794094e45024332a43b0600fa2b15d5bdd96d9a`. Each patch was applied cleanly to the published 22 source stack in isolated replay, with 274 Native and 563 C files byte-compared and zero mismatches. These are patch composition checks, not Linux build or execution results.

The draft PR gates run the Native queue/transport/output Go race cases, manager Go tests, Home Assistant service/framework tests, and the OwnTone C owner suite including `--queue-wire` and `--queue-connect-wire`. The final image Dockerfile also applies the exact patches and runs its packaged tests. Candidate versions here do not imply publication, installation, provider acceptance, or audible playback.
