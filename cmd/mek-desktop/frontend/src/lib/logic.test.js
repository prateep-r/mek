import { describe, expect, it } from "vitest";
import { filterAudit, filterKinds, stateTone, tags, when } from "./logic.js";

describe("filterKinds", () => {
  const kinds = [
    { Service: "ec2", Name: "classic-link-instances" },
    { Service: "ec2", Name: "instances" },
    { Service: "rds", Name: "db-instances" },
    { Service: "s3api", Name: "buckets" },
  ];
  it("keeps everything with no filter", () => expect(filterKinds(kinds, " ")).toEqual(kinds));
  it("puts an exact name first", () =>
    expect(filterKinds(kinds, "instances").map((k) => k.Name)).toEqual(["instances", "classic-link-instances", "db-instances"]));
  it("matches the service and both words", () => {
    expect(filterKinds(kinds, "s3api").map((k) => k.Name)).toEqual(["buckets"]);
    expect(filterKinds(kinds, "rds db").map((k) => k.Name)).toEqual(["db-instances"]);
  });
});

describe("filterAudit", () => {
  const es = [
    { context: "prod", decision: "blocked", command: ["aws", "s3", "rb"], target: "" },
    { context: "dev", decision: "allowed", command: ["aws", "s3", "ls"] },
    { context: "prod", decision: "declined", command: ["mek", "shell"], target: "bastion" },
  ];
  it("searches context, command and target", () => {
    expect(filterAudit(es, { query: "BASTION" })).toHaveLength(1);
    expect(filterAudit(es, { query: "s3" })).toHaveLength(2);
  });
  it("keeps problems only", () => expect(filterAudit(es, { problems: true }).map((e) => e.decision)).toEqual(["blocked", "declined"]));
  it("keeps one context", () => expect(filterAudit(es, { context: "dev" })).toHaveLength(1));
  it("keeps all by default", () => expect(filterAudit(es)).toHaveLength(3));
});

describe("when", () => {
  const now = new Date(2026, 9, 16, 18, 0, 0);
  it("shows today's time", () => expect(when(new Date(2026, 9, 16, 9, 5, 7).toISOString(), now)).toBe("09:05:07"));
  it("shows the date otherwise", () => expect(when(new Date(2026, 9, 2, 9, 5, 7).toISOString(), now)).toBe("Oct 02 09:05"));
});

describe("stateTone and tags", () => {
  it("colours states", () => {
    expect(stateTone("RUNNING")).toBe("good");
    expect(stateTone("exited (1)")).toBe("bad");
    expect(stateTone("stopped")).toBe("bad");
    expect(stateTone("pending")).toBe("");
    expect(stateTone(undefined)).toBe("");
  });
  it("tags guarded contexts", () => {
    expect(tags({ Protected: true, ReadOnly: true })).toEqual(["protected", "readonly"]);
    expect(tags({})).toEqual([]);
  });
});
