---
title: "Extending DPG"
description: "Use the pkg/dpg public API to extend DPG with custom linters, snapshot stores, and apply executors."
weight: 8
---

DPG exposes a stable public API in `pkg/dpg` for plugin authors. Extensions register with the DPG pipeline registry and are called during normal `dpg` command execution.

| Page | Description |
|------|-------------|
| [Pipeline Overview](overview/) | 6-stage pipeline, Registry pattern, extension points |
| [Plugin API](plugin-api/) | Writing linters, snapshot stores, apply executors, secret resolvers |
