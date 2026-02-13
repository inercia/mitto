import { FullConfig } from "@playwright/test";
import fs from "fs/promises";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Global setup for Playwright tests.
 * Runs once before all tests.
 */
async function globalSetup(config: FullConfig): Promise<void> {
  console.log("🔧 Running global setup...");

  const testDir = process.env.MITTO_DIR || "/tmp/mitto-test";

  // Create test directory structure
  await fs.rm(testDir, { recursive: true, force: true });
  await fs.mkdir(testDir, { recursive: true });
  await fs.mkdir(path.join(testDir, "sessions"), { recursive: true });

  // Get project root (two levels up from tests/ui)
  const projectRoot = path.resolve(__dirname, "../..");

  // Create test settings.json
  // Pass the scenarios directory to the mock ACP server with increased delay
  // to avoid race conditions where chunks arrive after prompt completes
  const scenariosDir = path.join(projectRoot, "tests/fixtures/responses");
  const mockAcpCommand = `${path.join(projectRoot, "tests/mocks/acp-server/mock-acp-server")} -scenarios ${scenariosDir} -delay 200ms`;
  const settings = {
    acp_servers: [
      {
        name: "mock-acp",
        command: mockAcpCommand,
      },
    ],
    web: {
      host: "127.0.0.1",
      port: 8089,
      theme: "v2",
    },
  };

  await fs.writeFile(
    path.join(testDir, "settings.json"),
    JSON.stringify(settings, null, 2),
  );

  // Create workspaces.json
  const workspaces = {
    workspaces: [
      {
        acp_server: "mock-acp",
        acp_command: mockAcpCommand,
        working_dir: path.join(
          projectRoot,
          "tests/fixtures/workspaces/project-alpha",
        ),
      },
    ],
  };

  await fs.writeFile(
    path.join(testDir, "workspaces.json"),
    JSON.stringify(workspaces, null, 2),
  );

  console.log(`✅ Test environment created at ${testDir}`);
}

export default globalSetup;
