import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";
import swagger2openapi from "swagger2openapi";

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, "..", "..");
const swaggerPath = path.join(repoRoot, "docs", "swagger.json");
const outDir = path.join(repoRoot, "web", "src", "lib", "generated");
const outPath = path.join(outDir, "api-types.ts");

const swagger = JSON.parse(await fs.readFile(swaggerPath, "utf8"));
const converted = await swagger2openapi.convertObj(swagger, { patch: true, warnOnly: true });
const ast = await openapiTS(converted.openapi, {
  alphabetize: true
});
const output = astToString(ast);

await fs.mkdir(outDir, { recursive: true });
await fs.writeFile(
  outPath,
  [
    "/* eslint-disable */",
    "// This file is generated from docs/swagger.json. Run `npm --prefix web run generate:api-types`.",
    "",
    output
  ].join("\n")
);
console.log(`Generated ${path.relative(repoRoot, outPath)}`);
