export type BadgeTone = "ok" | "warn" | "error" | "info" | "neutral" | "accent";

export function riskTone(risk?: string): BadgeTone {
  switch (risk) {
    case "dangerous":
    case "destructive":
      return "error";
    case "needs_confirmation":
      return "warn";
    case "safe":
      return "ok";
    default:
      return "neutral";
  }
}
