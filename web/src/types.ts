export interface VoiceConfig { voice_hint?: string; rate?: number; pitch?: number }
export interface ScriptMeta {
  id: string; title: string; players: number; lang: string; intro: string;
  rules?: string; credits?: string; source?: string; license_note?: string;
  series?: string; author?: string; difficulty?: string; tags?: string[]; duration_min?: number;
  points_label?: string; hide_points?: boolean; start_points?: number;
}
export interface PublicCharacter { id: string; name: string; public_intro: string; tts: VoiceConfig }
export interface ScriptEntry { meta: ScriptMeta; characters: PublicCharacter[] }
export interface Section { act: number; title: string; body: string }
export interface Skill { id: string; name: string; text: string; effect: string; cost?: number; exact_cost?: number; all_cost?: boolean; value?: number; target?: string; public?: boolean }
export interface CharacterView extends PublicCharacter {
  sections?: Section[]; goals?: string[]; performance?: string; ability?: string; can_lie: boolean;
  points?: number; floor?: string; letter?: string; skill?: Skill;
}
export interface Seat {
  id: string; name: string; char_name: string; public_intro: string; kind: string; joined: boolean; ready: boolean;
}
export interface PhaseView { id: string; type: string; title: string; index: number; act: number; deadline?: string }
export interface RoomInfo {
  id: string; script: string; title: string; phase: string | PhaseView; ended: boolean; seats: Seat[];
}
export interface Option { value: string; label: string }
export interface Field { key: string; label?: string; kind?: 'select' | 'text' | 'number' | string; options?: Option[]; required?: boolean; placeholder?: string }
export interface ActionSpec { type: string; desc: string; params?: Record<string, string>; options?: Option[]; fields?: Field[]; optional?: boolean }
export interface Action { type: string; payload?: Record<string, unknown>; state_id?: number }
export interface Panel { id: string; plugin: string; kind: string; title: string; phases?: string[]; data_key?: string; host_only?: boolean; order: number }
export interface Clue {
  id: string; location?: string; title?: string; body: string; act: number; visibility: string;
  kind?: string; color?: string; back?: string; floor?: string; image_url?: string; owner_role_id?: string;
  subject_role_id?: string; keywords?: string[]; keywords_all?: boolean; must_reveal?: boolean; extra_draw?: boolean; number?: number;
}
export interface PoolItem { id: string; title: string; kind?: string; number?: number }
export interface CluesData {
  mode?: 'pool' | 'location'; own?: Clue[]; public?: Clue[];
  locations?: { id: string; name: string; remaining: number }[];
  pool?: PoolItem[]; kinds?: Record<string, number>; ap?: number; quota?: number; picks_left?: number;
  current?: string; order?: string[]; items?: string[]; done?: boolean;
}
export interface TurnData { pid: string; round: number; total_rounds: number }
export interface VoteResult { question: string; winner: string; counts?: Record<string, number>; correct?: boolean }
export interface VotesData {
  questions?: { id: string; question: string; options: Option[] }[];
  cast?: Record<string, string>; results?: VoteResult[]; history?: VoteResult[];
}
export interface RevealData { truth?: string; title?: string; text?: string; results?: VoteResult[] }
export interface RankEntry { id: string; points: number }
export interface PointsSkill { id: string; name: string; text: string; effect: string; cost?: number; enabled: boolean }
export interface PointsData {
  points?: number; label?: string; skills?: PointsSkill[]; rank?: RankEntry[]; throne?: string;
  settled?: boolean; host?: RankEntry[];
}
export interface DiceRound { rolls?: Record<string, number>; asker?: string; answerer?: string; question?: string; answer?: string }
export interface DiceData {
  round?: number; total?: number; sides?: number; rolls?: Record<string, number>; asker?: string; answerer?: string;
  question?: string; answer?: string; questions?: string[]; keywords?: Record<string, string>; history?: DiceRound[]; waiting_for?: string;
}
export interface HandCard { id: string; kind: string; color?: string; number?: number; title?: string; text?: string }
export interface CardsData {
  hand?: HandCard[]; counts?: Record<string, number>; top?: string; color?: string; number?: number;
  current?: string; finished?: number; ranks?: string[]; persona?: string; words?: string[]; playable?: HandCard[];
  host_hands?: Record<string, HandCard[]>; score_note?: string;
}
export interface HostFact { id: string; text: string }
export interface HostData { dm_script?: string; facts?: HostFact[]; title?: string; hint?: string; vote_ids?: string[] }
export interface MediaData { audio_url?: string; audio_label?: string; title?: string }
export interface View {
  room_id: string; meta: ScriptMeta; state_id: number; last_seq: number; phase: PhaseView; ended: boolean;
  pid: string; seats: Seat[]; me?: CharacterView | null; allowed_actions: ActionSpec[]; hint?: string; panels: Panel[];
  data: Record<string, unknown> & {
    turn?: TurnData; clues?: CluesData; votes?: VotesData; reveal?: RevealData; points?: PointsData;
    dice?: DiceData; cards?: CardsData; host?: HostData; media?: MediaData;
  };
}
export interface GameEvent { seq: number; at: string; type: string; actor?: string; public: boolean; to?: string[]; payload?: Record<string, unknown> }
export type ServerMessage = { t: 'view'; view: View } | { t: 'event'; event: GameEvent } | { t: 'ack'; id: string; ok: boolean; error?: string };
export interface Session { room_id: string; token?: string; host_token?: string; player_id?: string }
export type SendAction = (type: string, payload?: Record<string, unknown>, host?: boolean) => Promise<void>;
export type Connection = 'connecting' | 'live' | 'reconnecting' | 'offline';
