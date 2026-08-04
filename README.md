# Neutrino ⚛️

> **Static Analysis & Introspection for Helm Charts, Zarf Packages, UDS
> Packages, and UDS Bundles.**

Neutrino is a developer tool that helps engineers working with **Helm charts**,
**Zarf packages**, and **UDS Bundles** understand and manage their configuration
surfaces. It statically analyzes Helm template ASTs to discover the _actual_
values used in templates, displays Zarf variables alongside chart values, and
provides both CLI and interactive TUI modes for exploring, searching, and
navigating configuration across the entire packaging stack.

### Why "Neutrino"?

Neutrinos pass through almost everything — yet we've built detectors to observe
them. Similarly, Helm values thread invisibly through layers of templates,
packages, and bundles. Neutrino makes those hidden configuration paths visible.

---

## 🚀 Why Neutrino?

Understanding the configurable surface of a Helm chart, Zarf package, or UDS
Bundle traditionally requires:

1. Reading `values.yaml` (which often contains unused defaults or misses hidden
   values).
2. Manually reading thousands of lines of Go template YAML spread across
   multiple charts and packages.
3. Cross-referencing Zarf variables, bundle overrides, and chart values by hand.

Neutrino solves this by acting as a compiler frontend. It parses templates just
like Helm does, walks the syntax tree, and tracks every reference to `.Values` —
then correlates those references with Zarf variables and package metadata.

**Key Capabilities:**

- **True Usage Detection**: Finds values used in templates, even if they aren't
  in `values.yaml`.
- **Dynamic Path Resolution**: Understands `range`, `with`, and variable
  assignments to resolve complex paths like `ingress.hosts.*.paths.*.path`.
- **Source Attribution**: Tells you exactly _where_ a value is used (file, line,
  column) and _how_ (direct field access vs. variable assignment).
- **Zarf Variable Mapping**: Surfaces Zarf package variables with their
  defaults, descriptions, and template locations.
- **UDS Bundle Override Analysis**: Cross-references hard-coded bundle override
  values and user-configurable override variables with the underlying chart
  defaults, making it easy to understand exactly what a bundle operator has
  locked down and what an end-user can still configure via `uds-config.yaml`.
- **Authoring Snippets**: For every Helm value in a UDS bundle context, the TUI
  detail view shows a ready-to-copy `uds-bundle.yaml` snippet for creating an
  override variable. For every UDS override variable, it shows the corresponding
  `uds-config.yaml` snippet — including the full YAML path and the chart's
  original default value.
- **Multi-Package Support**: Analyze entire Zarf packages and UDS Bundles in one
  command — all components and charts are inspected together.
- **Interactive TUI**: Explore values in a searchable, navigable terminal
  interface with support for filtering and in-editor jumping.

---

## 📦 Installation

Neutrino is written in Go and can be built from source.

### Prerequisites

- Go 1.25+

### Build from Source

```bash
# Clone the repository
git clone https://github.com/defenseunicorns-dashdays/neutrino.git
cd neutrino

# Build the binary
go build -o neutrino

# Verify installation
./neutrino --help
```

---

## 🛠 Usage

### `inspect`

The core command of Neutrino is `inspect`. It analyzes a package source and
outputs discovered overridable values, Zarf variables, and chart metadata.

#### Syntax

```bash
neutrino inspect [PACKAGE_SOURCE] [flags]
```

`[PACKAGE_SOURCE]` can be:

- A local helm chart directory path: `./charts/podinfo`
- A local helm chart archive: `./podinfo-6.11.1.tgz`
- A Zarf package archive: `./zarf-package-app-amd64-0.0.1.tar.zst`
- An OCI URL: `oci://ghcr.io/stefanprodan/charts/podinfo:6.11.1`
- An HTTP(S) URL: `https://example.com/charts/podinfo-6.11.1.tgz`
- A remote Zarf package: `oci://ghcr.io/zarf-dev/packages/init:v0.74.0`
- A UDS Bundle:
  `oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0`

#### Flags

| Flag                   | Type     | Default | Description                                           |
| :--------------------- | :------- | :------ | :---------------------------------------------------- |
| `--version`            | `string` |         | Version of the chart to fetch (when using URL).       |
| `--output`, `-o`       | `string` | `table` | Output format: `table`, `json`, `yaml`.               |
| `--include-subcharts`  | `bool`   | `true`  | Recursively analyze dependencies found in `charts/`.  |
| `--architecture`, `-a` | `string` |         | Architecture for OCI images and Zarf packages.        |
| `--interactive`, `-i`  | `bool`   | `false` | Enter interactive TUI mode.                           |
| `--sort-mode`          | `string` | `path`  | Sort mode for values: `path` or `discovery`.          |
| `--wildcards`          | `bool`   | `false` | Include wildcard/dynamic values in output.            |
| `--absolute`           | `bool`   | `false` | Output absolute filepaths for locations.              |
| `--no-values`          | `bool`   | `false` | Disable inference of nested paths from `values.yaml`. |
| `--no-cache`           | `bool`   | `false` | Force re-download of a package, ignoring cache.       |
| `--log-level`          | `string` | `info`  | Log level: `debug`, `info`, `warn`, `error`.          |

#### Examples

**1. Inspect a local chart directory:**

```bash
neutrino inspect ./charts/podinfo
```

**2. Inspect a local Zarf package directory (with `zarf.yaml`):**

```bash
neutrino inspect ./my-zarf-package/
```

**3. Output as JSON for automated processing:**

```bash
neutrino inspect ./charts/podinfo -o json > introspection.json
```

**4. Inspect an OCI chart:**

```bash
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo:6.11.1
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo --version 6.11.1
```

**5. Inspect a Zarf package:**

```bash
neutrino inspect ./zarf-package-podinfo-amd64-0.0.1.tar.zst
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0
```

**6. Inspect a UDS Bundle:**

```bash
neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0
neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/core-base:1.0.0-upstream -a amd64
```

**7. Interactive TUI mode:**

```bash
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0 -i
```

---

## :gear: CI Integration

Neutrino provides a GitHub Actions workflow for automated package change
analysis on pull requests.

- Automatic `neutrino diff` on PRs that modify package files
- Sticky PR comments with markdown diff tables and Mermaid diagrams
- Renovate and Dependabot PR detection with annotated context
- Optional CI gate mode (`--fail-on-warnings`) to block on airgap risks

Add the reusable workflow to your repository:

```yaml
jobs:
  diff:
    uses: defenseunicorns-dashdays/2026-03-uds-bundle-provenance/.github/workflows/neutrino-diff.yaml@main
    permissions:
      contents: read
      pull-requests: write
```

See the [CI Integration Guide](docs/ci-integration.md) for setup instructions
and configuration options.

---

## 📊 Understanding the Output

Neutrino classifies value usage into specific categories to help you understand
how flexibility is implemented in the chart.

### Output Types

1. **Field (`field`)**: A direct access to a value key.
   - _Code:_ `{{ .Values.image.repository }}`
   - _Result:_ `image.repository`

2. **Variable (`variable`)**: A value assigned to a variable, often passed to a
   template or helper.
   - _Code:_ `{{ $svc := .Values.service }} {{ $svc.port }}`
   - _Result:_ `service` (and potentially inferred `service.port`)

3. **Dynamic / Wildcard (`*`)**: Usage inside a `range` loop where keys are
   iterated.
   - _Code:_ `{{ range .Values.ingress.hosts }} - host: {{ .host }} {{ end }}`
   - _Result:_ `ingress.hosts.*.host`

When using the default `table` output, Neutrino groups repeated value paths to
keep the display clean. Locations are listed with their `file:line:column` for
easy navigation.

If inspecting a Zarf package or UDS Bundle, Neutrino also displays:

- A **Zarf Package Variables** table listing variables defined in `zarf.yaml`
  with their default values and descriptions.
- A per-component **Zarf Variables** table showing where `###ZARF_VAR_*###`
  template markers are used across chart templates.
- A **UDS Bundle Override Values** table listing all hard-coded value overrides
  set by the bundle (not user-configurable), alongside the chart's original
  defaults for comparison.
- A **UDS Bundle Override Variables** table listing all variables the bundle
  exposes for end-users to configure via `uds-config.yaml`, with both the
  bundle-operator default and the chart's original default.

### JSON Structure

When using `-o json`, the output provides a machine-readable schema containing
all components, their chart values, Zarf variables, and metadata:

```json
{
  "chartName": "podinfo",
  "chartVersion": "6.4.0",
  "source": "examples/helm-charts/chart",
  "values": [
    {
      "path": "ingress.hosts.*.paths.*.path",
      "chart": "podinfo",
      "wildcard": true,
      "locations": [
        {
          "file": "templates/ingress.yaml",
          "line": 32,
          "kind": "field"
        }
      ]
    }
  ]
}
```

---

## 📖 Documentation

Neutrino comes with a comprehensive documentation site built with MkDocs. This
site covers technical deep-dives into package handling, caching mechanisms, Helm
introspection, and known edge cases.

### Serving Docs Locally

To view the documentation site locally, ensure you have `mkdocs` installed (via
Homebrew is recommended) and run:

```bash
# Install mkdocs
brew install mkdocs-material

# Serve the documentation
mkdocs serve
```

The site will be available at `http://localhost:8000`.

---

## 🧩 Architecture

Neutrino utilizes the `text/template/parse` package from the Go standard library
to safely parse Helm templates. It mocks the Helm engine (including Sprig
functions and standard Helm helpers like `include` and `toYaml`) to ensure
templates parse successfully without needing a live Kubernetes cluster or a full
rendering context.

### Directory Structure

```
neutrino/
├── src/
│   ├── cmd/           # CLI command definitions (Cobra)
│   │   ├── root.go    # Root command
│   │   └── inspect.go # Inspection logic
│   ├── pkg/           # Core library code
│   │   ├── helm/
│   │   │   └── introspect/
│   │   │       ├── introspect.go # Main analysis orchestration
│   │   │       ├── walker.go     # AST traversal and state tracking
│   │   │       └── types.go      # Data structures
│   │   └── tui/       # Interactive terminal UI (Bubble Tea)
│   └── test/          # Test assets and E2E suites
└── main.go            # Entry point
```

---

## 🗺 Roadmap

- [x] **OCI Support**: Directly inspect charts from OCI registries
      (`oci://...`).
- [x] **Zarf Package Inspection**: Analyze `zarf.yaml` to map Zarf variables to
      Helm values.
- [x] **Interactive TUI**: Explore values in a searchable terminal interface.
- [x] **UDS Bundle Inspection**: Analyze UDS bundles containing multiple Zarf
      packages and their charts.
- [x] **UDS Bundle Override Mapping**: Deep analysis of UDS Bundle overrides to
      map configurable variables exposed by a bundle and streamline
      `uds-config.yaml` authoring.
- [x] **UDS Authoring Snippets**: Ready-to-copy `uds-bundle.yaml` and
      `uds-config.yaml` snippets surfaced in the TUI detail view to accelerate
      bundle authoring.
- [ ] **Local Directory Support**: Inspect local directories with `zarf.yaml`
      including recursive imports.
- [ ] **Linter Integration**: Warn about values documented in `values.yaml` but
      never used in templates.

---

## 🧪 Testing

Neutrino includes a comprehensive test suite, including local unit tests and
End-to-End (E2E) regression tests.

### Local Tests

Standard tests check core logic and local file introspection using lightweight
test assets found in `src/test/packages`. These run quickly and do not require
internet access.

```bash
go test ./...
```

### Full Regression (OCI) Tests

A subset of the E2E suite verifies Neutrino against real-world, multi-gigabyte
OCI artifacts (including large UDS bundles and Zarf init packages).

**Note:** These tests are gated behind the `e2e` build tag because they require
significant bandwidth and time to download external OCI layers.

To run the full regression suite:

```bash
go test -v ./src/test/e2e/... -tags e2e
```

---

## 🤝 Contributing

Contributions are welcome!

1. Fork the repo.
2. Create your feature branch (`git checkout -b feature/amazing-feature`).
3. Commit your changes (`git commit -m 'Add some amazing feature'`).
4. Push to the branch (`git push origin feature/amazing-feature`).
5. Open a Pull Request.
