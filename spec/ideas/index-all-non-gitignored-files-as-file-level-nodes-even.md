---
captured_by: specstudio:implement
status: queued
---
# Index all non-gitignored files as file-level nodes (even unknown extensions like *.txt), not just recognized source languages

Today ScanDirectory filters candidates through IsSourceFile (extract.DetectLanguage != LangUnknown), so files with unrecognized extensions get no node at all. Idea: emit a file-level node for every git-visible (non-gitignored) file, regardless of language, so the graph reflects the whole repo. Design questions: graph/DB bloat, binary blobs, size caps, value of bare file nodes, and it would rebaseline every golden.
