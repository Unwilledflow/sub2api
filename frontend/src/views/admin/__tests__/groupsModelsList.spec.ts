import { describe, expect, it } from "vitest";

import {
  addCustomModelAllowlistItem,
  buildModelAllowlistConfig,
  buildModelsListConfig,
  createModelsListState,
  type ModelAllowlistAddError,
  hydrateModelsListState,
  invertModelsListSelection,
  moveModelsListItem,
  selectAllModelsListItems,
  setModelsListCandidates,
  toggleModelsListItem,
} from "../groupsModelsList";

describe("groupsModelsList", () => {
  it("selects all default candidates for a new disabled config", () => {
    const state = createModelsListState();

    setModelsListCandidates(state, ["gpt-5.5", "gpt-5.4"]);

    expect(state.enabled).toBe(false);
    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: true },
    ]);
  });

  it("keeps saved selections and marks new candidates as unselected when editing", () => {
    const state = createModelsListState({
      enabled: true,
      models: ["gpt-5.5", "gpt-5.4"],
    });

    setModelsListCandidates(state, ["gpt-5.4", "legacy-gpt", "gpt-5.5"]);

    expect(state.enabled).toBe(true);
    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: true },
      { id: "legacy-gpt", selected: false },
    ]);
  });

  it("preserves explicitly unselected saved candidates when candidates refresh", () => {
    const state = createModelsListState({
      enabled: true,
      models: ["gpt-5.5"],
    });

    setModelsListCandidates(state, ["gpt-5.5", "gpt-5.4"]);

    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: false },
    ]);
  });

  it("builds config with selected models in current display order", () => {
    const state = hydrateModelsListState({
      enabled: true,
      models: ["gpt-5.5", "gpt-5.4", "legacy-gpt"],
    }, ["gpt-5.5", "gpt-5.4", "legacy-gpt"]);

    toggleModelsListItem(state, "legacy-gpt");
    moveModelsListItem(state, 1, 0);

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      models: ["gpt-5.4", "gpt-5.5"],
    });
  });

  it("keeps selected models in payload even when disabled so reopening can restore choices", () => {
    const state = hydrateModelsListState({
      enabled: false,
      models: ["gpt-5.5"],
    }, ["gpt-5.5", "gpt-5.4"]);

    expect(buildModelsListConfig(state)).toEqual({
      enabled: false,
      models: ["gpt-5.5"],
    });
  });

  it("preserves saved models when candidates have not loaded yet", () => {
    const state = createModelsListState({
      enabled: true,
      models: ["gpt-5.5", "gpt-5.4"],
    });

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      models: ["gpt-5.5", "gpt-5.4"],
    });
  });

  it("selects all candidate models from the toolbar action", () => {
    const state = hydrateModelsListState({
      enabled: true,
      models: ["gpt-5.5"],
    }, ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"]);

    selectAllModelsListItems(state);

    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: true },
      { id: "gpt-5.4", selected: true },
      { id: "gpt-5.4-mini", selected: true },
    ]);
  });

  it("inverts selected models from the toolbar action", () => {
    const state = hydrateModelsListState({
      enabled: true,
      models: ["gpt-5.5"],
    }, ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"]);

    invertModelsListSelection(state);

    expect(state.items).toEqual([
      { id: "gpt-5.5", selected: false },
      { id: "gpt-5.4", selected: true },
      { id: "gpt-5.4-mini", selected: true },
    ]);
  });
});

describe("addCustomModelAllowlistItem", () => {
  const state = () => createModelsListState({ enabled: true, models: ["gpt-5.4"] });

  it("appends a trimmed entry as selected", () => {
    const s = state();
    expect(addCustomModelAllowlistItem(s, "  claude-sonnet-4.5  ")).toBeNull();
    expect(s.items.at(-1)).toEqual({ id: "claude-sonnet-4.5", selected: true });
  });

  it("accepts trailing wildcard entries", () => {
    const s = state();
    expect(addCustomModelAllowlistItem(s, "gpt-5.5-*")).toBeNull();
    expect(s.items.at(-1)?.id).toBe("gpt-5.5-*");
  });

  it("rejects blank input", () => {
    expect(addCustomModelAllowlistItem(state(), "   ")).toBe<ModelAllowlistAddError>("empty");
  });

  it("rejects wildcards that are not trailing", () => {
    expect(addCustomModelAllowlistItem(state(), "gpt-*-5.4")).toBe<ModelAllowlistAddError>("invalid_wildcard");
    expect(addCustomModelAllowlistItem(state(), "gpt-*-codex-*")).toBe<ModelAllowlistAddError>("invalid_wildcard");
  });

  it("rejects duplicates case-insensitively", () => {
    const s = state();
    expect(addCustomModelAllowlistItem(s, "GPT-5.4")).toBe<ModelAllowlistAddError>("duplicate");
  });
});

describe("single model allowlist separators", () => {
  it.each(["gpt-fixture,", "gpt-fixture;", ",gpt-fixture,;\t\n", "gpt-fixture\n"])(
    "normalizes a single pasted model: %j", (raw) => {
      const state = createModelsListState();
      expect(addCustomModelAllowlistItem(state, raw)).toBeNull();
      expect(buildModelAllowlistConfig(state).models).toEqual(["gpt-fixture"]);
    },
  );

  it.each([",;\t\n", " , ; "])("rejects delimiter-only input: %j", (raw) => {
    const state = createModelsListState();
    expect(addCustomModelAllowlistItem(state, raw)).toBe("empty");
    expect(state.items).toEqual([]);
  });

  it("checks duplicates and wildcard placement after normalizing separators", () => {
    const state = createModelsListState({ models: ["gpt-fixture"] });
    expect(addCustomModelAllowlistItem(state, "GPT-fixture,")).toBe("duplicate");
    expect(addCustomModelAllowlistItem(state, "gpt-*-fixture;")).toBe("invalid_wildcard");
    expect(addCustomModelAllowlistItem(state, "gpt-fixture-*;")).toBeNull();
    expect(state.items).toEqual([{ id: "gpt-fixture-*", selected: true }]);
  });

  it("preserves bulk order while skipping duplicates and invalid entries", () => {
    const state = createModelsListState({ models: ["saved-model"] });
    expect(addCustomModelAllowlistItem(state, "saved-model,first-model;second-model\nFIRST-MODEL bad*entry")).toBeNull();
    expect(buildModelAllowlistConfig(state).models).toEqual(["first-model", "second-model"]);
  });
});
