package prompt

import "fmt"

func Build(issueID string, title string, description string, acceptance string) string {
	return fmt.Sprintf(`You are in YOLO mode - all permissions granted.

Your task is: %s - %s

**Description:**
%s

**Acceptance Criteria:**
%s

**Strict TDD Protocol:**
1. Write failing tests based on acceptance criteria
2. Run tests to confirm they fail
3. Write minimal implementation to pass each test
4. Run tests and ensure all pass
5. Do not modify unrelated files
6. If tests fail, fix and rerun

**Rules:**
- NEVER write implementation code before a failing test exists
- Watch test fail before writing code
- Write minimal code to pass each test
- Do not modify unrelated files
- Use real code, not mocks unless unavoidable
- All tests must pass before marking task complete

Start now by analyzing the codebase and writing your first failing test.
`, issueID, title, description, acceptance)
}
