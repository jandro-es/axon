# Diagrams

System diagrams for AXON, in two formats from a single source:

| Diagram | Editable source | Rendered (embedded in docs) |
|---------|-----------------|-----------------------------|
| System architecture | [`architecture.excalidraw`](architecture.excalidraw) | [`architecture.svg`](architecture.svg) |
| Knowledge ingestion pipeline | [`ingestion-pipeline.excalidraw`](ingestion-pipeline.excalidraw) | [`ingestion-pipeline.svg`](ingestion-pipeline.svg) |
| Token chokepoint & automation lifecycle | [`token-chokepoint.excalidraw`](token-chokepoint.excalidraw) | [`token-chokepoint.svg`](token-chokepoint.svg) |
| Personal memory & identity (Phase 8) | [`personal-memory.excalidraw`](personal-memory.excalidraw) | [`personal-memory.svg`](personal-memory.svg) |
| Multi-client (Claude Desktop, Phase 9) | [`multi-client.excalidraw`](multi-client.excalidraw) | [`multi-client.svg`](multi-client.svg) |

- **`.excalidraw`** — open and edit at [excalidraw.com](https://excalidraw.com)
  (drag-and-drop the file) or with the Excalidraw VS Code extension.
- **`.svg`** — what the README and the [GUIDE](../GUIDE.md) embed (GitHub renders
  SVG inline).

Both are generated from one spec so they never drift. To change a diagram, edit
[`generate.mjs`](generate.mjs) and regenerate:

```bash
node docs/diagrams/generate.mjs
```
