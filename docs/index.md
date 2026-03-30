# Neutrino

Neutrino is an introspection and management tool built for discovering, resolving, and visualizing the inner workings of Helm charts, Zarf packages, and UDS bundles.

It is designed to give developers and operators crystal-clear insight into exactly how their infrastructure-as-code deployments are structured, without requiring them to download massive archives or run complex commands.

## Why Neutrino?

In complex application deployments relying on Kubernetes (via Helm) and air-gapped package management (via Zarf and UDS), distinguishing exactly what configurations, variables, and default values exist in an upstream dependency can be challenging.

Neutrino solves this by standardizing introspection across these packaging layers:

1. **UDS Bundles**: Top-level packages containing multiple Zarf packages.
2. **Zarf Packages**: Intermediate packages containing multiple Helm charts and components.
3. **Helm Charts**: The lowest level deployment unit, containing templates and parameterizable `values.yaml` files.

Instead of making you fully pull a repository, untar its contents, and manually read `values.yaml` or template files, Neutrino:
- Transparently fetches exactly what it needs (often using OCI layer isolation).
- Simulates template execution to discover what dynamic paths exist and how they bind to defaults.
- Compiles a unified map of variables and configuration options up the hierarchy.

## Lean by Design

Neutrino is built to be fast, cache-friendly, and portable. It automatically avoids heavy networking operations when it doesn't need to rebuild intermediate abstractions. Throughout these docs, we cover how Neutrino differentiates between input formats, how its caching system accelerates workflows, and how the internal Helm walker unearths variables that traditional parsing misses.
