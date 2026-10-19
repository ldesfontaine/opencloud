// Le ton d'un état : ce qui va, ce qui demande attention, ce qui est en danger.
export type StateTone = "ok" | "warn" | "danger";

// Le ton d'une pastille : un état, ou une teinte sans verdict.
export type Tone = StateTone | "neutral" | "accent";

// Le ton d'un segment de frise : un état, ou rien quand il n'y a rien à dire.
export type SegmentTone = StateTone | "none";
