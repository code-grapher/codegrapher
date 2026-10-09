# MeaningGraph and ModelSpec indexing

CodeGrapher indexes `.meaning.yaml` and `.modelspec.hcl` / `.modelspec.json`
using the released MeaningGraph and ModelSpec parsers. Declarations become typed
nodes (`meaning_graph`, `meaning_concept`, `model_module`, `model_entity`,
`model_component`, `model_enum`, and `model_member`). Containment, declared
references, compositions, concept extension, values, units, measures, and
accepted `binds_to` relationships become edges. Binding edge metadata records its role, match, and note.

ModelSpec files are read in the current spelling (`record`, `field`,
`record =`; JSON format `1.0-draft-2`) and in the earlier one (`entity`,
`property`, `entity =`; JSON format `1.0-draft`), and MeaningGraph bindings
resolve against a model in either. Both spellings extract to the same nodes and
edges, `binds_to` included: a record type is written as `model_entity`, and a
reference member carries the `entity` attribute, whichever spelling the file
uses. A file in the earlier spelling adds one `deprecated-spelling` warning to
its module's `metadata.diagnostics`, once per file, naming
`modelspec rewrite --write` with the repository-relative path; it never fails
indexing. A meaning graph that lists a model file that is not indexed keeps the
warning on the graph node instead, so one model file never carries the same
notice twice. The removed `collection` and `recordset` constructs and the
reserved words `projection`, `index` and `migration` are refused by the parser
(an error diagnostic on the module naming the word, whichever file of the module
holds it), so `model_collection` and `model_recordset` nodes are no longer
produced.

Semantic IDs use the graph or module scope and declaration name, so moving a
declaration between files in the same scope keeps its ID. A ModelSpec HCL/JSON
twin is one logical node with `metadata.representations` listing both source
locations. Parse, validation, duplicate, unresolved-reference, and twin-drift
findings are retained in node `metadata.diagnostics`. External concept
references retain their requested repository and revision in
`metadata.unresolvedReferences`; only locally declared, unambiguous targets
resolve. Indexing never fetches an external repository implicitly.

Source mappings require an explicit comment immediately before the code
declaration (within three lines), for example:

```go
// modelspec: implements chinook.Invoice.total
func InvoiceTotal() int { return 0 }

// meaninggraph: references invoice-total
func ShowInvoiceTotal() {}
```

The comment must name exactly one semantic target. `implements` and
`references` describe different claims; CodeGrapher does not infer a mapping
from matching names. An accepted mapping emits `maps_to_code` with provenance
`explicit_annotation` and annotation source evidence in edge metadata. Its
target is a `semantic_code` bridge whose `metadata.canonicalCodeId` points to
the actual code symbol in its owning language scope. Consumers should follow
that canonical ID for code navigation and deduplicate symbol displays by it.

The CLI, graph store, snapshot/INGR export, and browser API retain semantic
node and edge metadata. Browser graph edges can share endpoints while carrying
different binding roles or source evidence; each distinct declaration remains
visible. Extraction version 15 rebuilds existing indexes so the semantic
projection is populated after upgrade.
