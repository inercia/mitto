/**
 * Unit tests for SlashCommandPicker component logic
 *
 * Tests cover:
 * - Command filtering by name prefix
 * - Command selection handling
 * - Edge cases (empty commands, no matches)
 */

// =============================================================================
// Command Filtering Logic Tests
// =============================================================================

/**
 * Filter commands by name prefix (case-insensitive).
 * This is the core filtering logic used by SlashCommandPicker.
 */
function filterCommands(commands, filter) {
  return commands.filter((cmd) =>
    cmd.name.toLowerCase().startsWith(filter.toLowerCase()),
  );
}

describe("SlashCommandPicker filtering logic", () => {
  const sampleCommands = [
    { name: "test", description: "Run tests" },
    { name: "help", description: "Show help" },
    { name: "health", description: "Health check" },
    { name: "Task", description: "Create task" }, // Capital T for case-insensitive test
    { name: "web", description: "Web search" },
  ];

  describe("filterCommands", () => {
    test("returns all commands when filter is empty", () => {
      const result = filterCommands(sampleCommands, "");
      expect(result).toHaveLength(5);
    });

    test("filters commands by exact prefix match", () => {
      const result = filterCommands(sampleCommands, "h");
      expect(result).toHaveLength(2);
      expect(result.map((c) => c.name)).toContain("help");
      expect(result.map((c) => c.name)).toContain("health");
    });

    test("filters commands case-insensitively", () => {
      const result = filterCommands(sampleCommands, "T");
      expect(result).toHaveLength(2);
      expect(result.map((c) => c.name)).toContain("test");
      expect(result.map((c) => c.name)).toContain("Task");
    });

    test("filters commands with lowercase filter matching uppercase name", () => {
      const result = filterCommands(sampleCommands, "task");
      expect(result).toHaveLength(1);
      expect(result[0].name).toBe("Task");
    });

    test("returns empty array when no commands match", () => {
      const result = filterCommands(sampleCommands, "xyz");
      expect(result).toHaveLength(0);
    });

    test("filters by multi-character prefix", () => {
      const result = filterCommands(sampleCommands, "hea");
      expect(result).toHaveLength(1);
      expect(result[0].name).toBe("health");
    });

    test("filters full command name", () => {
      const result = filterCommands(sampleCommands, "test");
      expect(result).toHaveLength(1);
      expect(result[0].name).toBe("test");
    });

    test("returns empty array for empty commands list", () => {
      const result = filterCommands([], "test");
      expect(result).toHaveLength(0);
    });

    test("does not match commands that contain but don't start with filter", () => {
      // "health" contains "ea" but doesn't start with it
      const result = filterCommands(sampleCommands, "ea");
      expect(result).toHaveLength(0);
    });
  });
});

// =============================================================================
// Slash Filter Extraction Tests
// =============================================================================

/**
 * Extract the slash command filter from input text.
 * Returns the text after '/' but before any space, when input starts with '/'.
 */
function extractSlashFilter(text, showSlashPicker) {
  return text.startsWith("/") && showSlashPicker
    ? text.slice(1).split(/\s/)[0]
    : "";
}

describe("Slash filter extraction", () => {
  test("extracts filter when text starts with / and picker is shown", () => {
    expect(extractSlashFilter("/test", true)).toBe("test");
  });

  test("returns empty when picker is not shown", () => {
    expect(extractSlashFilter("/test", false)).toBe("");
  });

  test("extracts only text before space", () => {
    expect(extractSlashFilter("/help some args", true)).toBe("help");
  });

  test("returns empty for text not starting with /", () => {
    expect(extractSlashFilter("test", true)).toBe("");
  });

  test("returns empty string for just /", () => {
    expect(extractSlashFilter("/", true)).toBe("");
  });

  test("handles multiple spaces correctly", () => {
    expect(extractSlashFilter("/cmd  arg1  arg2", true)).toBe("cmd");
  });
});

// =============================================================================
// Slash Command Picker Visibility Logic Tests
// =============================================================================

/**
 * Determine if the slash command picker should be shown based on input.
 */
function shouldShowSlashPicker(text, availableCommandsLength) {
  return (
    text.startsWith("/") && availableCommandsLength > 0 && !text.includes(" ")
  );
}

describe("Slash picker visibility logic", () => {
  test("shows picker when text starts with / and commands available", () => {
    expect(shouldShowSlashPicker("/t", 5)).toBe(true);
  });

  test("hides picker when text has space (command complete)", () => {
    expect(shouldShowSlashPicker("/test arg", 5)).toBe(false);
  });

  test("hides picker when no commands available", () => {
    expect(shouldShowSlashPicker("/test", 0)).toBe(false);
  });

  test("hides picker for text not starting with /", () => {
    expect(shouldShowSlashPicker("test", 5)).toBe(false);
  });

  test("shows picker for just /", () => {
    expect(shouldShowSlashPicker("/", 5)).toBe(true);
  });
});

