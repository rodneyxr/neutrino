# Neutrino

Neutrino statically inspects package and bundle configuration. Artifact and inspection terminology lives in [CONTEXT.md](CONTEXT.md).

## Language

**Zarf Package Values**:
Structured configuration owned by a Zarf package, with packaged defaults, ordered operator inputs, and an optional schema. These inputs are distinct from Helm chart defaults, Zarf legacy variables, and UDS bundle variables.
_Avoid_: Helm defaults, bundle variables

**Package Values mapping**:
A package-owned declaration relating a Package Values source path to a Helm chart target path, with optional excluded source paths. A mapping describes configuration reachability, not a deployment result or schema approval.
_Avoid_: applied override, effective chart default
