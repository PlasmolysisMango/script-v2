export interface VoiceConfig { voice_hint?: string; rate?: number; pitch?: number }
export interface ScriptMeta {
  id: string; title: string; players: number; lang: string; intro: string;
  rules?: string; credits?: string; source?: string; license_note?: string;
}
export interface PublicCharacter { id: string; name: string; public_intro: string; tts: VoiceConfig }
export interface ScriptEntry { meta: ScriptMeta; characters: PublicCharacter[] }
export interface Section { act: number; title: string; body: string }
export interface CharacterView extends PublicCharacter {
  sections?: Section[]; goals?: string[]; performance?: string; ability?: string; can_lie: boolean;
}
export interface Seat {
  id: string; name: string; char_name: string; public_intro: string; kind: string; joined: boolean; ready: boolean;
}
export interface PhaseView { id: string; type: string; title: string; index: number; act: number; deadline?: string }
export interface RoomInfo {
  id: string; script: string; title: string; phase: string | PhaseView; ended: boolean; seats: Seat[];
}
export interface Option { value: string; label: string }
export interface ActionSpec { type: string; desc: string; params?: Record<string, string>; options?: Option[]; optional?: boolean }
export interface Action { type: string; payload?: Record<string, unknown>; state_id?: number }
export interface Panel { id: string; plugin: string; kind: string; title: string; phases?: string[]; data_key?: string; order: number }
export interface Clue { id: string; location: string; title?: string; body: string; act: number; visibility: string }
export interface CluesData { own?: Clue[]; public?: Clue[]; locations?: { id: string; name: string; remaining: number }[]; ap?: number }
export interface TurnData { pid: string; round: number; total_rounds: number }
export interface VotesData {
  questions?: { id: string; question: string; options: Option[] }[];
  cast?: Record<string, string>; results?: unknown[];
}
export interface RevealData { truth?: string; title?: string; text?: string; results?: unknown[] }
export interface View {
  room_id: string; meta: ScriptMeta; state_id: number; last_seq: number; phase: PhaseView; ended: boolean;
  pid: string; seats: Seat[]; me?: CharacterView | null; allowed_actions: ActionSpec[]; hint?: string; panels: Panel[];
  data: Record<string, unknown> & { turn?: TurnData; clues?: CluesData; votes?: VotesData; reveal?: RevealData };
}
export interface GameEvent { seq: number; at: string; type: string; actor?: string; public: boolean; to?: string[]; payload?: Record<string, unknown> }
export type ServerMessage = { t: 'view'; view: View } | { t: 'event'; event: GameEvent } | { t: 'ack'; id: string; ok: boolean; error?: string };
export interface Session { room_id: string; token?: string; host_token?: string; player_id?: string }
export type SendAction = (type: string, payload?: Record<string, unknown>, host?: boolean) => Promise<void>;
export type Connection = 'connecting' | 'live' | 'reconnecting' | 'offline';
