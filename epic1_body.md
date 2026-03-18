## Overview
Extract the task executing agent from embedded code into a configurable YAML/JSON-based system that defines tools, pipeline steps, transitions, and conditions.

## Goals
- Move agent logic from code to external configuration
- Support configurable pipeline: Quality Gate → Execution → QC Gate → Finish/Retry
- Enable non-coding executors in the future (docs, testing, etc.)
- Define unified agent description format for ALL backends (codex, opencode, claude, kimi, gemini)

## Acceptance Criteria
- [ ] Task executor configuration is external (YAML/JSON)
- [ ] Built-in agents (codex, opencode, claude, kimi) moved to unified config format
- [ ] Pipeline supports: quality gate, execution, qc gate, retry with addendum
- [ ] Configuration validation prevents invalid pipelines
- [ ] Documentation for creating custom executors

## Technical Details
### Pipeline Stages
1. **Quality Gate**: Validate task clarity (description, AC, dependencies)
2. **Execution**: Run agent with configured backend
3. **QC Gate**: Validate results (tests pass, review passes)
4. **Completion**: Close task OR retry with addendum

### Configuration Format Example
See docs for executor configuration schema with stages, tools, and transitions.

## Dependencies
None - foundational epic

## Notes
- This enables v3.0 architecture
- Must maintain backward compatibility during migration
- Focus on coding executors first, extensible for other types
