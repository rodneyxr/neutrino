# UDS CLI Next bundles and Zarf Package Values: Neutrino research

**Research snapshot: 2026-09-23.** This is an implementation/research reference, not a proposed Neutrino design. External assertions are linked to first-party Zarf or UDS CLI documentation/source. “Current Zarf docs” means the documentation page served on this date (its version selector listed latest **v0.85.0**); Neutrino currently imports **Zarf v0.82.0**, released 2026-07-23. A behavior described only by current docs is therefore not asserted to be identical in v0.82.0 unless the v0.82.0 release/source evidence below supports it.

## Scope and status

### Facts

- **Zarf Package Values** are structured YAML data declared through top-level `zarf.yaml` `values.files` and optionally `values.schema`; they use Go templates and differ from legacy `###ZARF_VAR_*###` string substitution. [Zarf Package Values](https://docs.zarf.dev/ref/package-values/#defining-values)
- The current official example is titled **“[BETA] Values & Templates Example”** and explicitly keeps constants and variables available until they are fully deprecated. It proves present coexistence in that example, but the main reference does not independently label the entire feature beta or name a removal release. [Zarf values-templating example](https://docs.zarf.dev/ref/examples/values-templating/)
- Neutrino pins `github.com/zarf-dev/zarf v0.82.0` in [`go.mod`](../../../go.mod#L5-L14). Zarf’s first-party v0.82.0 release is dated **2026-07-23** and calls out a breaking change in `set-values` CLI type inference. [Zarf v0.82.0 release](https://github.com/zarf-dev/zarf/releases/tag/v0.82.0)
- UDS CLI’s accepted **Bundles Next** ADR is dated 2026-01-22 and updated 2026-08-27. It replaces legacy `uds-bundle.yaml` bundle definitions with HCL2 `bundle.uds.hcl`, delegates bespoke overrides to Zarf-native namespace/values features, and states that legacy bundles must be migrated. [UDS Bundles Next ADR](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md)
- UDS CLI's current README says Legacy remains the default while Next is an alpha preview behind `NextMode`, with beta default and Legacy removal planned after a migration window. [UDS CLI modes and quickstarts](https://github.com/defenseunicorns/uds-cli/blob/main/README.md#cli-modes) The separate prior note, [`uds-cli-next-bundles-research.md`](uds-cli-next-bundles-research.md), provides additional UDS Next context, but its bundle-author conversion example does not describe this repository: Neutrino is an inspector, and its checked-in bundles are test fixtures under [`src/test/bundles/`](../../../src/test/bundles/), not an authored production bundle.

### Version uncertainty to preserve

The current Zarf reference is the strongest source for semantics, but its rendered version selector is latest v0.85.0. Neutrino’s v0.82.0 release proves a `set-values` type-inference change existed by that release, not every current-doc detail (for example, every template object/function or later validation behavior). Before making an inspection claim depend on a Package Values field, validate its exact v0.82.0 API/source or upgrade deliberately. This is an evidence boundary, not an assertion that the feature is absent in v0.82.0.

## Zarf configuration surfaces

| Surface | What Zarf documents | Consumer-facing consequence |
| --- | --- | --- |
| Legacy package variables | A top-level `variables` declaration supplies named string-oriented values; `###ZARF_VAR_<NAME>###` can appear in manifests, files, and charts. A chart can map a variable to a Helm `path`. Variables can also be populated from action output via `setVariables`. [Deployment Variables](https://docs.zarf.dev/ref/values/#using-variables) · [chart mapping](https://docs.zarf.dev/ref/values/#helm-chart-mapping) · [setVariables](https://docs.zarf.dev/ref/values/#variables-zarf_var_) | The existing Neutrino variable model is about declared variables and marker occurrences, not a general template evaluator. |
| Package Values files | `values.files` identifies YAML files; several files merge in declared order and a later file wins. `values.schema` may name a JSON schema validated for values supplied at deploy time. [values files and schema](https://docs.zarf.dev/ref/package-values/#values-files) | Package-level defaults are a new structured input distinct from a Helm chart’s own `values.yaml` and from chart `valuesFiles`. |
| Deploy-time Package Values | `-v`/`--values` accepts one or more values files. `--set-values` accepts dotted `key.path=value`; booleans and base-10 whole numbers are inferred, decimals/leading-zero numbers remain strings, quoted values force strings, and lists/other complex types must be supplied in a values file. [setting values](https://docs.zarf.dev/ref/package-values/#setting-values) | An artifact inspection can report packaged defaults/mappings, but cannot truthfully report an effective deploy-time value without the operator’s files and flags. |
| Package Values precedence | Lowest to highest: package `values.files` (each in order), deploy-time `-v`/`--values` files (each in order), then `--set-values`. [precedence](https://docs.zarf.dev/ref/package-values/#precedence) | Preserve source order and provenance; flattening to one “default” loses override semantics. |
| Manifests and actions | Manifests require `template: true`; individual action commands require `template: true`. An action `setValues` writes dotted keys from command output (`string`, `json`, or `yaml`) for later actions/manifests in the same deployment. [manifest/actions/setValues](https://docs.zarf.dev/ref/package-values/#in-manifests) | Dynamic action-produced values are runtime state, not package defaults. Static inspection may identify the declaration but must not fabricate a resulting value. |
| Helm mapping | A chart’s `values` mappings transfer Package Values `sourcePath` to chart `targetPath`; mappings run top-to-bottom and a later mapping to the same target wins. `.` maps a root, and `excludePaths` omits descendants. [Helm mapping behavior](https://docs.zarf.dev/ref/package-values/#mapping-behavior) | A Package Value can influence a chart value without appearing in a legacy variable marker or a chart values file. The mapping itself is necessary provenance. |
| Templated Helm values file | `templatedValuesFiles` renders a Helm values file with Zarf Go templating at deploy time and exposes `.Values`, `.State`, and documented functions; Helm charts themselves retain Helm templating. [templated values files](https://docs.zarf.dev/ref/package-values/#templated-values-files) · [templating boundary](https://docs.zarf.dev/ref/package-values/#templating) | The file is not a static Helm values document until its deployment context is known. It must remain distinguished from ordinary `valuesFiles`. |

## Values versus legacy variables: verified coexistence constraints

### Facts

1. Migration guidance says legacy variables are string substitutions, while Package Values are maps rendered with Go templates. It gives a direct migration from `###ZARF_VAR_DATABASE_USERNAME###` to `{{ .Values.database.username }}` and requires `template: true` for a manifest. [Variables to Values: manifests/files](https://docs.zarf.dev/best-practices/variables-to-values/#migrating-manifests-and-files)
2. For Helm, the same guidance directs authors away from substituting a variable into chart `valuesFiles` or chart `variables.path`, toward package-value `sourcePath` → `targetPath` mappings; where Zarf templating is truly needed inside a values file, it directs use of `templatedValuesFiles`. [Variables to Values: Helm](https://docs.zarf.dev/best-practices/variables-to-values/#migrating-helm-charts)
3. Legacy action command substitution is automatic; Package Values action commands must opt into templating. The documented action-output migration is `setVariables.name` → `setValues.key`. [Variables to Values: actions](https://docs.zarf.dev/best-practices/variables-to-values/#migrating-actions)
4. A legacy variable `pattern` maps to a JSON-schema regex under `values.schema`. Legacy constants do not have a direct equivalent: the official guidance suggests schema `const`, a static chart `valuesFiles` value, or data from `.Pkg`, depending on intent. [Variables to Values: validation/constants](https://docs.zarf.dev/best-practices/variables-to-values/#variable-validation)
5. The official beta example deliberately declares both `values` and legacy `constants`/`variables`, and says the latter are available in Go templates until fully deprecated. [values-templating example](https://docs.zarf.dev/ref/examples/values-templating/)

### Migration implications — inference, not product promises

- A one-to-one “variable renamed to value” presentation would be wrong: a value can be structured, schema-constrained, overridden by a file, mapped to many chart targets, or produced during deployment. A legacy variable is a named substitution surface. Neutrino needs their distinct provenance and must not collapse them merely because both can affect a Helm value.
- Do not infer a final value for `templatedValuesFiles`, action `setValues`, `.State`, or a missing deploy-time values file during static inspection. The documentation explicitly makes these deployment-context/runtime features. [Package Values templating](https://docs.zarf.dev/ref/package-values/#templating)
- A migration may retain a legacy variable and add a Package Value in the same artifact. Any user interface must identify both independently, including their separate operator input channels, until legacy support is actually removed upstream.

## UDS Next and Zarf Package Values

### Facts from the accepted UDS primary source

- A Next `package` block has a `source`; `optional_components` is selected at create time; `depends_on` expresses package ordering. The ADR’s `namespace` and `values_files` are direct passthroughs to Zarf-native configuration. [Bundles Next block definitions](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md#bundleudshcl)
- The ADR says `values_files` keys must match the target Zarf package’s value interface. Its stored bundle layout preserves these files as ordered `values/<package_id>/<number>.yaml` entries. [Bundles Next package example/layout](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md#bundleudshcl) · [OCI structure](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md#oci-structure)
- The same ADR says bundle `config.uds.hcl` variables template bundle values files and that **top-level scalar** variables are passed through to Zarf `###ZARF_VAR_*###` substitutions; non-scalar variables are only supported in `values_files` templates. It defines defaults→config nested-object deep merge, while scalar/list/set/tuple values replace wholesale. [Bundles Next deploy-time configuration](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md#configudshcl-deploy-time-configuration)
- This is coexistence, not automatic translation: the accepted ADR’s consequence is “No backward compatibility: Existing `uds-bundle.yaml` bundles must be migrated.” [Bundles Next consequences](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md#negative)

### Additional UDS package guidance — unreleased

UDS’s `main` Package Values guide is explicitly marked **unreleased** (it links to v1-13 as latest release), so it is useful compatibility evidence but not a released UDS CLI commitment. It advises retaining `###ZARF_VAR_<NAME>###` in ordinary chart `valuesFiles` only when compatibility requires it; when explicit package values must take precedence or need a computed fallback, it advises `templatedValuesFiles` with `.Variables.<NAME>`. Its stated reason is an ordering constraint: Zarf substitutes variable tokens before it evaluates Go templates. [UDS Package Values guide](https://docs.defenseunicorns.com/core/main/concepts/configuration--packaging/package-values/)

### Practical coexistence matrix

| Artifact/workflow path | Legacy UDS bundle overrides | Zarf legacy variables | Zarf Package Values | What Neutrino can presently evidence | Constraint/caveat |
| --- | --- | --- | --- | --- | --- |
| Legacy `uds-bundle.yaml` + legacy Zarf package | Yes: `overrides.<component>.<chart>.values` and `.variables` | Yes | May coexist in the package, but legacy bundle parsing does not turn them into UDS overrides | It parses/attaches bundle fixed values and exposed variables, and reports package variables. [`uds.go`](../../../src/pkg/helm/introspect/uds.go#L21-L59) and [override processing](../../../src/pkg/helm/introspect/uds.go#L440-L503) | A Package Value mapping is not a legacy UDS override; do not label it as one. |
| Direct legacy-variable Zarf package | N/A | Yes | Optional coexistence | It unmarshals `zarf.yaml`, returns `pkg.Variables`, and correlates discovered marker usages/defaults. [`zarf.go`](../../../src/pkg/helm/introspect/zarf.go#L91-L100) and [variable mapping](../../../src/pkg/helm/introspect/zarf.go#L223-L242) | This model does not claim to evaluate variable substitution. |
| Direct Zarf package with Package Values | N/A | Optional coexistence | Yes | Current code still discovers charts and chart-level `ValuesFiles`/`TemplatedValuesFiles`. [`zarf.go`](../../../src/pkg/helm/introspect/zarf.go#L190-L215) | It does **not** inspect package-level `values.files`, `values.schema`, chart `values` mappings, or their effective merge. See gap below. |
| UDS Next bundle + Zarf package values | Legacy YAML overrides are intentionally replaced by native Zarf inputs | Top-level scalar Next config variables may still be passed to `###ZARF_VAR_*###` | Bundle `values_files` are Zarf value inputs | No established Next bundle reader/format handling is evidenced in this repository | Bundle values-file templating and Zarf Package Values are separate layers. Do not assume their precedence or templates are interchangeable beyond the ADR’s stated passthrough. |
| UDS Next bundle + mixed Zarf package | Not the Next mechanism | Still supported during upstream transition | Present in current Zarf docs; beta labeling applies to the cited example, not conclusively the feature | No current end-to-end Neutrino representation | A scalar config value can feed a legacy Zarf variable while a bundle values file feeds Package Values; preserve both paths and their origins. |

## Current Neutrino evidence and concrete gaps

### Facts from this checkout

- The command classifies an input as Zarf or UDS and publishes `Variables`, `UDSOverrideValues`, and `UDSOverrideVariables` in the inspection report/output. [`src/cmd/inspect.go`](../../../src/cmd/inspect.go#L96-L125) · [table output](../../../src/cmd/inspect.go#L232-L280)
- The legacy UDS parser represents only package identity and `Overrides`, whose chart object contains `Values` and `Variables`. It records configured bundle values/variables with package/component/chart context and looks up the original chart default. [`src/pkg/helm/introspect/uds.go`](../../../src/pkg/helm/introspect/uds.go#L21-L59) · [override processing](../../../src/pkg/helm/introspect/uds.go#L440-L503)
- Local/direct Zarf inspection unmarshals the upstream `v1alpha1.ZarfPackage`, then finds component charts. For each chart it gathers **chart-level** `chart.ValuesFiles` and `chart.TemplatedValuesFiles` and passes only found files into Helm introspection. [`src/pkg/helm/introspect/zarf.go`](../../../src/pkg/helm/introspect/zarf.go#L105-L215)
- The manifest-first remote Package Reader requires `zarf.yaml`, then selects one `components/<component>.tar` only for components that declare charts. It selects neither a package-level values-file descriptor nor a package-level schema descriptor. [`src/pkg/helm/introspect/package_reader.go`](../../../src/pkg/helm/introspect/package_reader.go#L139-L208)
- This differs from the accepted local architecture decision: its Package View is intended to contain every declared chart and **managed values file**. [`docs/adr/0001-manifest-first-package-reader.md`](../../../docs/adr/0001-manifest-first-package-reader.md#L1-L5) The precise meaning of “managed values file” must be resolved against upstream’s package-level `values.files` as well as chart-level files before claiming compliance.

### Consequences for future work — inference

1. **Model at least three configuration layers.** Preserve separate identities for (a) chart defaults/chart-level files, (b) package-level values and schema, and (c) bundle-supplied values/configuration. Within a layer, preserve declared ordering and mapping order; across layers, report only precedence documented by the owning tool.
2. **Capture mappings, not only files.** A complete Package Values inspection surface needs `sourcePath`, `targetPath`, `excludePaths`, declaration order, and the owning component/chart, because mappings may override one another without a values file changing.
3. **Keep runtime values unevaluated.** `setValues`, templated values files, `.State`, and deployer-provided `--values`/`--set-values` must be represented as runtime/operation inputs unless exact deployment inputs and an intentional evaluator are available.
4. **Do not redefine old outputs.** Existing `UDSOverrideValue` means a fixed legacy `uds-bundle.yaml` override and `UDSOverrideVariable` means a legacy `uds-config.yaml`-configurable path; both comments and data types state that boundary. [`types.go`](../../../src/pkg/helm/introspect/types.go#L82-L106) New Package Values provenance should not be presented as either type.
5. **New remote descriptors are required for static package-value evidence.** Because Zarf’s values files/schema are named from `zarf.yaml`, a manifest-first reader that omits their descriptor titles cannot inspect packaged defaults/schema. This follows directly from the current selection code and the external `values.files` contract; it is not a claim about the exact artifact title/layout until confirmed against the pinned Zarf package layout source.

## Questions requiring primary-source verification before implementation

1. Which Package Values fields and behavior are present in the exact Zarf **v0.82.0** API imported by Neutrino, versus documented only by current v0.85.0 docs?
2. In a packaged OCI Zarf artifact, what exact descriptor titles/layout paths do `values.files` and `values.schema` receive in v0.82.0, and are they always independently addressable? The reader must use proven layout rules, not path guesses.
3. Does Zarf validate schema before or after all deploy-time merges and action-produced `setValues`, and which inputs are validated on removal as well as deployment?
4. How does UDS Next order its `values_files` relative to direct Zarf `-v`/`--values` and `--set-values` inputs in the implemented, released CLI? The ADR demonstrates passthrough but is not a substitute for command implementation/released reference semantics.
5. Which UDS Next bundle artifact media types and config/value layers does the version Neutrino intends to support publish? The current legacy UDS detector/reader must not classify by an unverified Next layout.

## Source index

### First-party external sources

- [Zarf Package Values reference](https://docs.zarf.dev/ref/package-values/) — current docs, accessed 2026-09-23; values files/schema, merge order, CLI flags, precedence, templates, mappings, and `setValues`.
- [Zarf Variables to Values migration guide](https://docs.zarf.dev/best-practices/variables-to-values/) — current docs, accessed 2026-09-23; coexistence/migration distinctions.
- [Zarf Deployment Variables reference](https://docs.zarf.dev/ref/values/) — current docs, accessed 2026-09-23; legacy marker, chart mapping, and `setVariables` behavior.
- [Zarf values-templating example](https://docs.zarf.dev/ref/examples/values-templating/) — current docs, accessed 2026-09-23; beta status and live mixed values/variables example.
- [Zarf v0.82.0 release](https://github.com/zarf-dev/zarf/releases/tag/v0.82.0) — 2026-07-23; pinned-version release evidence.
- [UDS CLI Bundles Next accepted ADR](https://github.com/defenseunicorns/uds-cli/blob/main/docs/adr/0001-bundles-next-spec.md) — updated 2026-08-27; Next bundle/source/value passthrough and coexistence evidence.
- [UDS CLI README, CLI modes](https://github.com/defenseunicorns/uds-cli/blob/main/README.md#cli-modes) — current `main`, accessed 2026-09-23; Legacy default, Next alpha behind feature flag, planned beta transition.
- [UDS Package Values guide (unreleased `main`)](https://docs.defenseunicorns.com/core/main/concepts/configuration--packaging/package-values/) — accessed 2026-09-23; UDS-specific variable/values compatibility and substitution-order guidance. It must not be treated as released behavior.

### Neutrino repository evidence

All internal implementation citations above are relative links pinned to the present checkout. They describe existing behavior only; they are not external product guarantees.
