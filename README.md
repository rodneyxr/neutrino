# Neutrino ⚛️

> **Static Analysis & Introspection for Helm Charts and Zarf Packages.**

Neutrino is a standalone command-line tool designed to provide deep insights
into Helm charts and Zarf packages. Unlike simple linters or text search tools,
Neutrino statically analyzes the Abstract Syntax Tree (AST) of Helm templates to
discover **actual usage** of values, uncovering the true configuration surface
of a chart.

---

## 🚀 Why Neutrino?

Traditionally, understanding which values can be overridden in a Helm chart
relies on:

1. Reading `values.yaml` (which often contains unused defaults or misses hidden
   values).
2. Manually reading thousands of lines of Go templates YAML.

Neutrino solves this by acting as a compiler frontend. It parses the templates
just like Helm does, walks the syntax tree, and tracks every reference to
`.Values`.

**Key Capabilities:**

- **True Usage Detection**: Finds values used in templates, even if they aren't
  in `values.yaml`.
- **Dynamic Path Resolution**: Understands `range`, `with`, and variable
  assignments to resolve complex paths like `ingress.hosts.*.paths.*.path`.
- **Source Attribution**: Tells you exactly _where_ a value is used (file, line
  number) and _how_ (direct field access vs. variable assignment).

---

## 📦 Installation

Neutrino is written in Go and can be built from source.

### Prerequisites

- Go 1.25+

### Build from Source

```bash
# Clone the repository
git clone https://github.com/rodneyxr/neutrino.git
cd neutrino

# Build the binary
go build -o neutrino

# Verify installation
./neutrino --help
```

---

## 🛠 Usage

### `inspect`

The core command of Neutrino is `inspect`. It analyzes a chart and outputs the
discovered overridable values.

#### Syntax

```bash
neutrino inspect [PACKAGE_SOURCE] [flags]
```

`[PACKAGE_SOURCE]` can be:

- A local directory path: `./charts/podinfo`
- A local archive: `./podinfo-6.11.1.tgz`
- An OCI URL: `oci://ghcr.io/stefanprodan/charts/podinfo`
- An HTTP(S) URL: `https://example.com/charts/podinfo-6.11.1.tgz`
- A Zarf package: `./zarf-package-app-amd64-0.0.1.tar.zst`
- A UDS Bundle: `oci://ghcr.io/defenseunicorns/packages/uds/core:1.0.0-upstream`

#### Flags

| Flag                   | Type     | Default | Description                                          |
| :--------------------- | :------- | :------ | :--------------------------------------------------- |
| `--version`            | `string` |         | Version of the chart to fetch (when using URL).      |
| `--output`, `-o`       | `string` | `table` | Output format: `table`, `json`, `yaml`.              |
| `--include-subcharts`  | `bool`   | `true`  | Recursively analyze dependencies found in `charts/`. |
| `--architecture`, `-a` | `string` |         | Architecture for OCI images and Zarf packages.       |

#### Examples

**1. Inspect a local chart directory:**

```bash
neutrino inspect ./charts/podinfo
```

**2. Output as JSON for automated processing:**

```bash
neutrino inspect ./charts/podinfo -o json > introspection.json
```

**3. Inspect an OCI chart:**

```bash
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo:6.11.1
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo --version 6.11.1
```

**4. Inspect a Zarf package:**

```bash
neutrino inspect ./zarf-package-podinfo-amd64-0.0.1.tar.zst
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0
```

**5. Inspect an OCI Zarf package (specific architecture):**

```bash
neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/core-base:1.0.0-upstream -a amd64
```

**6. Inspect a UDS Bundle:**

```bash
neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0
```

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
keep the display clean. Locations are listed with their `file:line` for easy
navigation.

If inspecting a Zarf package, Neutrino also displays a **Zarf Package
Variables** table, listing any variables defined in `zarf.yaml` along with their
default values and descriptions.

### JSON Structure

When using `-o json`, the output provides a machine-readable schema:

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
brew install mkdocs

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
├── cmd/           # CLI command definitions (Cobra)
│   ├── root.go    # Root command
│   └── inspect.go # Inspection logic
├── pkg/           # Core library code
│   └── helm/
│       └── introspect/
│           ├── introspect.go # Main analysis orchestration
│           ├── walker.go     # AST traversal and state tracking
│           └── types.go      # Data structures
└── main.go        # Entry point
```

---

## 🗺 Roadmap

- [x] **OCI Support**: Directly inspect charts from OCI registries
      (`oci://...`).
- [x] **Zarf Package Inspection**: Analyze `zarf.yaml` to map Zarf variables to
      Helm values.
- [x] **Interactive Mode**: TUI for exploring values and generating
      `values.yaml` files interactively.
- [ ] **UDS Bundle Inspection**: Analyze UDS bundles to map UDS variables to
      Helm values.
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
