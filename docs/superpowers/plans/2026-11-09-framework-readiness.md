# Generic framework readiness implementation plan

Goal: close the previously reproduced WiFi ownership gap and make existing browser verification and managed integration reproducible.
Scope: reusable router module only; no nixos-home changes or physical gateway operations. Preserve the full home-router objective; these fixes do not complete advanced capabilities.
Design: extend the existing IPv4/DHCP ownership check to the explicitly managed radio, reuse the two existing browser suites in CI with a pinned Playwright dependency, and document managed integration separately from historical generate-only guidance. Runtime networking behavior is unchanged.

- [x] Add module regression cases: managed radio IPv4 and DHCP rejected, unrelated radio allowed, absent radio allowed, preview allowed. Prove RED then GREEN.
- [x] Add pinned browser test dependencies and CI using a freshly built backend; retain existing Go/VM checks. Run both suites with the same dependency locally.
- [x] Add a generic managed example and lifecycle/ownership documentation; label old candidate evidence historical.
- [x] Run module evaluations, browser regressions, diff checks and independent review; record results.

Review focus: no accidental rejection of unrelated NICs or preview deployments; empty wifiInterface must remain valid; CI must run both suites and fail on failures; browser secrets/state remain temporary; docs must not imply host-specific migration or hardware validation.

## Verification record

- Baseline refreshed from origin/main: 6cd6df9; original workspace user changes preserved.
- RED: new WiFi ownership regression failed on the existing assertion (no managed-interface conflict detected).
- GREEN: all six WiFi cases and previous managed/legacy module assertions returned true. Preview fixture initially conflicted with the test baseline; fixed its override using mkForce, without weakening product assertions.
- Locked Playwright 1.64.1 installed successfully using npm ci --offline. Both browser suites passed 34/34 (70.73s), with the newly built backend and installed local Chromium. CI installs its matching Playwright Chromium on Ubuntu; remote CI result remains to be verified after pushing.
- Embedded/standalone assets identical; git diff --check passed.
- Independent review found no blocking issues. Its unrelated-radio test suggestion was incorporated to exercise both managed wlan0 and unmanaged wlan9 together; module suite then passed.
- No physical gateway or nixos-home changes. These close the three framework follow-ups; the broader home-router objective, including deferred advanced capabilities, is not declared complete.
