const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");

const script = fs.readFileSync(path.join(__dirname, "upstream-agent-lifecycle-capture.sh"), "utf8");
const start = script.indexOf('const projectsDir = path.join(configDir, "projects");');
const end = script.indexOf("const requestSummaries =", start);
assert.ok(start >= 0 && end > start, "capture transcript discovery must exist");
const discovery = script.slice(start, end) + "\n({ transcriptPath, transcriptExists });";

function discover(directories, files, exists = true) {
  const projectRoot = "/capture/config/projects";
  return vm.runInNewContext(discovery, {
    path,
    configDir: "/capture/config",
    sessionId: "test-session",
    fs: {
      existsSync: (candidate) => candidate === projectRoot ? exists : files.includes(candidate),
      readdirSync: () => directories.map(([name, isDirectory]) => ({
        name, isDirectory: () => isDirectory
      }))
    }
  });
}

test("finds the session under an arbitrary project slug", () => {
  const file = "/capture/config/projects/arbitrary-project/test-session.jsonl";
  const result = discover([["arbitrary-project", true], ["unrelated-file", false]], [file]);
  assert.equal(result.transcriptPath, file);
  assert.equal(result.transcriptExists, true);
});

test("missing projects directory or session is reported as absent", () => {
  assert.equal(discover([], [], false).transcriptPath, null);
  assert.equal(discover([["another-project", true]], []).transcriptExists, false);
});

test("multiple matching transcripts are rejected instead of misattributed", () => {
  assert.throws(() => discover([["one", true], ["two", true]], [
    "/capture/config/projects/one/test-session.jsonl",
    "/capture/config/projects/two/test-session.jsonl"
  ]), /ambiguous transcript/);
});
