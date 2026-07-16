import assert from "node:assert/strict";
import test from "node:test";

import { renderCliProcessFailure } from "../node-src/cli/process-failure.js";
import { ProcessFailure } from "../node-src/domain/errors.js";

test("CLI ProcessFailure rendering preserves every operator-facing field", () => {
  const rendered = renderCliProcessFailure(new ProcessFailure({
    category: "domain",
    code: "EXAMPLE_FAILURE",
    details: [{
      code: "INVALID_FIELD",
      message: "must be set\nfor this provider",
      path: "items.example.field",
    }],
    message: "operation failed\nwithout exposing raw state",
    retryable: true,
  }));

  assert.equal(rendered, [
    "error: operation failed",
    "  without exposing raw state",
    "  code: EXAMPLE_FAILURE",
    "  category: domain",
    "  retryable: yes",
    "  detail: items.example.field [INVALID_FIELD] must be set",
    "  for this provider",
    "",
  ].join("\n"));
});
