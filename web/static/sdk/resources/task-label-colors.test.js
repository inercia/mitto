import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createTaskLabelColorsResource } from "./task-label-colors.js";

const mk = resourceMounter((config) => ({
  taskLabelColors: createTaskLabelColorsResource(config),
}));

describe("task label colors resource", () => {
  test("getGlobal() calls the dedicated global endpoint", async () => {
    const { taskLabelColors, calls } = mk();
    await taskLabelColors.getGlobal();
    expect(calls[0].url).toBe("/api/global/task-label-colors");
    expect(calls[0].init.method).toBe("GET");
  });

  test("setGlobal() PUTs the ordered entries body", async () => {
    const { taskLabelColors, calls } = mk();
    const body = {
      entries: [
        { label: "needs-human", color: "#ef4444" },
        { label: "blocked", color: "#f59e0b" },
      ],
    };
    await taskLabelColors.setGlobal(body);
    expect(calls[0].url).toBe("/api/global/task-label-colors");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  test("supports clearing all mappings", async () => {
    const { taskLabelColors, calls } = mk();
    await taskLabelColors.setGlobal({ entries: [] });
    expect(calls[0].init.body).toBe('{"entries":[]}');
  });

  test("surfaces non-2xx responses", async () => {
    const { taskLabelColors, respondWith } = mk();
    respondWith(() =>
      fakeResponse({
        status: 400,
        body: { error: { code: "invalid_argument", message: "invalid color" } },
      }),
    );
    await expect(taskLabelColors.setGlobal({ entries: [] })).rejects.toThrow(
      "invalid color",
    );
  });
});
