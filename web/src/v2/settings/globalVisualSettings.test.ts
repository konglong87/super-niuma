import { describe, expect, it } from "vitest";
import { DEFAULT_APPEARANCE, DEFAULT_PET, readVisualSettings, visualSettingsStyle } from "./globalVisualSettings";

describe("global visual settings", () => {
  it("uses stable defaults and preserves valid appearance and pet values", () => {
    const settings = readVisualSettings({
      appearance: {
        enabled: true,
        backgroundColor: "#123456",
        backgroundImage: "/assets/backgrounds/quiet.jpg",
        overlayOpacity: 0.35,
        sidebarSurfaceOpacity: 0.76,
        emptyBlur: 4,
        conversationBlur: 7,
        composerBlur: 3,
        bubbleOpacity: 0.8
      },
      pet: {
        enabled: true,
        visible: false,
        model: "go-companion",
        scale: 1.25,
        right: 42,
        bottom: 18,
        animation: "focus",
        statusBubble: false,
        fontScale: 1.1
      }
    });

    expect(settings.appearance).toEqual({
      enabled: true,
      backgroundColor: "#123456",
      backgroundImage: "/assets/backgrounds/quiet.jpg",
      overlayOpacity: 0.35,
      sidebarSurfaceOpacity: 0.76,
      emptyBlur: 4,
      conversationBlur: 7,
      composerBlur: 3,
      bubbleOpacity: 0.8
    });
    expect(settings.pet).toEqual({
      enabled: true,
      visible: false,
      model: "go-companion",
      scale: 1.25,
      right: 42,
      bottom: 18,
      animation: "focus",
      statusBubble: false,
      fontScale: 1.1
    });
  });

  it("defaults new installations to the Niuma companion while preserving selectable legacy pets", () => {
    expect(DEFAULT_PET.model).toBe("niuma");
    expect(readVisualSettings({}).pet.model).toBe("niuma");
    for (const model of ["niuma", "niuma-classic", "go-companion", "chinese-dragon"]) {
      expect(readVisualSettings({ pet: { model } }).pet.model).toBe(model);
    }
  });

  it("rejects unsafe visual values and clamps numeric values without changing unrelated settings", () => {
    const settings = readVisualSettings({
      provider: "custom",
      appearance: { backgroundColor: "red", backgroundImage: "javascript:alert(1)", overlayOpacity: 2, sidebarSurfaceOpacity: 2, emptyBlur: -4, bubbleOpacity: 0 },
      pet: { scale: 9, right: -10, bottom: 1000, fontScale: 0.1 }
    });

    expect(settings.appearance).toMatchObject({
      ...DEFAULT_APPEARANCE,
      backgroundImage: "",
      overlayOpacity: 1,
      sidebarSurfaceOpacity: 1,
      bubbleOpacity: 0.2
    });
    expect(settings.pet).toMatchObject({
      ...DEFAULT_PET,
      scale: 2,
      right: 0,
      bottom: 160,
      fontScale: 0.75
    });
  });

  it("maps saved values to page CSS variables", () => {
    const style = visualSettingsStyle(readVisualSettings({ appearance: { backgroundColor: "#123456", overlayOpacity: 0.2, sidebarSurfaceOpacity: 0.76, emptyBlur: 5 } }));
    const variables = style as Record<string, unknown>;
    expect(variables["--webui2-visual-background-color"]).toBe("#123456");
    expect(variables["--webui2-visual-overlay-opacity"]).toBe("0.2");
    expect(variables["--webui2-sidebar-surface-opacity"]).toBe("76%");
    expect(variables["--webui2-visual-empty-blur"]).toBe("5px");
  });
});
