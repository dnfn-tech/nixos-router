# System SSH and plugin framework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Remove router-owned SSH configuration and prove a reusable plugin contract with real AdBlock DNS behavior.
**Architecture:** Pure module registry/validation/rendering shared by API and executor; activation stays within the existing managed generation transaction. System SSH ports enter firewall policy through deployment options only.
**Tech Stack:** Go, native JavaScript, NixOS modules, NixOS Python VM driver.
**Spec:** docs/superpowers/specs/2026-11-12-plugin-framework-system-ssh-design.md

## Global Constraints

- No physical gw deployment, host network mutations, commits or publication in this task.
- Preserve revision concurrency, secret references, read-only last-good recovery and ownership isolation.
- Synchronize web/ and backend/web/; no third-party dynamic plugin execution or subscription downloads.

## Review Focus

- Historical SSH fields in saved files, backups and executor recovery must not become effective settings.
- Unknown disabled plugin configuration remains exportable without permitting runtime execution.
- Disabled local DNS and nonempty legacy subscriptions must fail before network changes.
- Plugin failure cannot become a successful apply or overwrite confirmed runtime state.
- System-managed overlay services and SSH must not be stopped by plugin configuration.

### Task 1: SSH migration and removal

Files: backend/internal/config/{document.go,draft.go,types.go}, backend/internal/managed/{render.go,manager.go}, backend/internal/server/server.go, backend/cmd/routerd/main.go, modules/managed.nix, web/app.js, backend/web/app.js, schema and fixtures located by literal references.
Interfaces: CurrentSchemaVersion=2; legacy document reads discard ssh; strict new writes reject ssh. System SSHPorts remains deployment-only, empty means none.
- [x] Write migration/strict-write/read-only recovery tests and run them RED.
- [x] Implement schema v2 migration; remove SSH model/UI/status; update fixtures and system-port parsing.
- [x] Run config, command, managed and server tests GREEN; validate browser old-link behavior and system-port module contract.

### Task 2: Module registry and AdBlock

Files: new backend/internal/modules/{registry.go,adblock.go,registry_test.go}; config plugin validation adapter; managed rendering; server plugin API and capability response; Nix declarations and CLI wiring.
Interfaces: modules.Descriptor; modules.Lookup(id); modules.All(); modules.Validate(id string, enabled bool, values map[string]interface{}, localDNS bool) error; modules.RenderDNS(values map[string]interface{}) (string,error). Registry never accepts HTTP-defined implementations.
- [x] Write RED tests for duplicate/invalid registration, AdBlock rules, unknown enabled modules, unsupported subscriptions and DNS dependency.
- [x] Implement deterministic registry and typed field validation with 4096 entries maximum, 253-byte domain maximum, ASCII DNS labels, normalized deduplication, A/AAAA+subdomain block.
- [x] Connect module validation to all config writes and managed validation; render into owned dnsmasq generation; configure common allowed modules for API/executor.
- [x] Make plugin API and navigation registry-driven with explicit support and observed-state boundaries; ensure system services cannot be controlled via unsupported modules.
- [x] Run focused Go suites and then complete Go suite.

### Task 3: UI and isolated acceptance

Files: web/app.js, backend/web/app.js, tests/browser regression files, new tests/vm-managed-adblock.nix and tests/managed-adblock.py, flake.nix, docs/plugins.md and schema documentation.
Interfaces: Existing plugins config API and apply jobs; test module enables AdBlock on the standard managed-router topology.
- [x] Add browser RED tests for removed SSH entry, module availability, AdBlock entries and revision-safe save/apply.
- [x] Implement UI using registry capability data; hide unsupported toggles and remove subscription editor.
- [x] Add VM A/AAAA, subdomain, disable, replace, invalid-rule, executor restart and full reboot assertions driven by real HTTP config/apply.
- [x] Run browser suite, Nix module evaluations, new VM and affected router/privsep/IPv6 regressions; Go race/vet and asset synchronization.
- [x] Update documentation with actual results and outstanding checks, obtain independent final review and fix substantive findings with RED/GREEN tests.

## Execution notes

User approved design and said continue; implement inline in the existing isolated worktree. Do not request a second method approval. Keep changes uncommitted as specified in approved design. Candidate f7905d02 is the baseline, not proof of the new version.

## 验证结论

实现与验证完成，记录见 `docs/plugin-framework-acceptance.md`。独立审查的一项 P2 已经 RED/GREEN 回归修复；完整 Go/race/vet、50 项浏览器、两组模块断言、五组相关 VM 和隔离 gw 构建通过。race 初次累计超时后以 30 分钟时限重跑通过。按已批准范围保留未提交，不推送、不部署设备。
