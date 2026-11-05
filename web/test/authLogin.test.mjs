import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/store/authModel.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ES2022,
    target: ts.ScriptTarget.ES2022,
  },
});
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.outputText).toString("base64")}`;
const { isStaleAuthRequest, loginIdentityFromResponse } = await import(moduleURL);

test("successful login response is authoritative even without a session lookup", () => {
  assert.deepEqual(
    loginIdentityFromResponse(
      {
        status: "ok",
        authenticated: true,
        user: { username: "admin" },
      },
      "fallback",
    ),
    { username: "admin", role: "Administrator" },
  );

  assert.deepEqual(
    loginIdentityFromResponse(
      {
        status: "ok",
        authenticated: true,
        username: "operator",
        role: "Operator",
      },
      "fallback",
    ),
    { username: "operator", role: "Operator" },
  );

  assert.deepEqual(
    loginIdentityFromResponse({ status: "ok", authenticated: true }, "admin"),
    { username: "admin", role: "Administrator" },
  );
});

test("stale unauthorized responses cannot overwrite a newer password login", () => {
  assert.equal(isStaleAuthRequest(1000, 2000), true);
  assert.equal(isStaleAuthRequest(2001, 2000), false);
  assert.equal(isStaleAuthRequest(undefined, 2000), false);
  assert.equal(isStaleAuthRequest(1000, 0), false);
});
