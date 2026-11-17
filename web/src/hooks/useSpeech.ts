import { useCallback, useEffect, useRef, useState } from 'react';
import type { GameEvent, VoiceConfig } from '../types';

type SpeechItem = { text: string; voice: VoiceConfig };
export function chooseChineseVoice(voices: SpeechSynthesisVoice[], hint?: string): SpeechSynthesisVoice | undefined {
  const chinese = voices.filter(voice => /^(zh|cmn|yue)(-|_|$)/i.test(voice.lang));
  return (hint ? chinese.find(voice => voice.name.toLowerCase().includes(hint.toLowerCase())) : undefined)
    || chinese.find(voice => /zh[-_]CN/i.test(voice.lang)) || chinese[0];
}
export function eventSpeech(event: GameEvent): SpeechItem | null {
  if (event.type !== 'tts' && !event.type.endsWith('.tts')) return null;
  const payload = event.payload || {};
  if (typeof payload.text !== 'string' || !payload.text.trim()) return null;
  const nested = payload.tts || payload.voice;
  const config = nested && typeof nested === 'object' ? nested as VoiceConfig : payload as VoiceConfig;
  return { text: payload.text, voice: config };
}
const bounded = (value: number | undefined, fallback: number, min: number, max: number) =>
  typeof value === 'number' && Number.isFinite(value) ? Math.max(min, Math.min(max, value)) : fallback;
export function useSpeech() {
  const supported = typeof window !== 'undefined' && !!window.speechSynthesis && typeof window.SpeechSynthesisUtterance === 'function';
  const [enabled, setEnabled] = useState(false);
  const [speaking, setSpeaking] = useState(false);
  const [error, setError] = useState('');
  const [voiceCount, setVoiceCount] = useState(0);
  const [rate, setRate] = useState(1);
  const rateRef = useRef(1);
  const enabledRef = useRef(false);
  const queue = useRef<SpeechItem[]>([]);
  const utterance = useRef<SpeechSynthesisUtterance | null>(null);
  const pumpRef = useRef<() => void>(() => {});
  const stop = useCallback(() => {
    queue.current = [];
    if (utterance.current) { utterance.current.onend = null; utterance.current.onerror = null; }
    utterance.current = null;
    if (supported) window.speechSynthesis.cancel();
    setSpeaking(false);
  }, [supported]);
  const pump = useCallback(() => {
    if (!supported || !enabledRef.current || utterance.current || !queue.current.length) return;
    const item = queue.current.shift()!;
    const voice = chooseChineseVoice(window.speechSynthesis.getVoices(), item.voice.voice_hint);
    if (!voice) {
      queue.current = [];
      setError('浏览器未提供中文语音。请在系统中安装中文语音，或改用文字阅读。');
      return;
    }
    const next = new SpeechSynthesisUtterance(item.text);
    next.lang = voice.lang; next.voice = voice;
    next.rate = bounded(bounded(item.voice.rate, 1, 0.1, 10) * rateRef.current, 1, 0.1, 10);
    next.pitch = bounded(item.voice.pitch, 1, 0, 2);
    next.onend = () => {
      if (utterance.current !== next) return;
      utterance.current = null; setSpeaking(false); pumpRef.current();
    };
    next.onerror = event => {
      if (utterance.current !== next) return;
      utterance.current = null; queue.current = []; setSpeaking(false);
      if (event.error !== 'canceled' && event.error !== 'interrupted') setError(`朗读未完成（${event.error}），可重新点击朗读或使用文字。`);
    };
    utterance.current = next; setSpeaking(true); setError('');
    try { window.speechSynthesis.speak(next); }
    catch { utterance.current = null; queue.current = []; setSpeaking(false); setError('浏览器拒绝朗读，请检查语音权限后重试。'); }
  }, [supported]);
  pumpRef.current = pump;
  useEffect(() => {
    if (!supported) return;
    const update = () => {
      setVoiceCount(window.speechSynthesis.getVoices().filter(voice => /^(zh|cmn|yue)(-|_|$)/i.test(voice.lang)).length);
    };
    update();
    window.speechSynthesis.addEventListener('voiceschanged', update);
    return () => {
      window.speechSynthesis.removeEventListener('voiceschanged', update);
      queue.current = []; enabledRef.current = false;
      if (utterance.current) {
        utterance.current.onend = null; utterance.current.onerror = null;
        window.speechSynthesis.cancel(); utterance.current = null;
      }
    };
  }, [supported]);
  const toggle = () => {
    if (!supported) { setError('此浏览器不支持 Web Speech 语音朗读，请使用文字阅读。'); return; }
    const next = !enabledRef.current;
    enabledRef.current = next; setEnabled(next); setError('');
    if (!next) stop();
    else {
      window.speechSynthesis.getVoices();
      window.speechSynthesis.resume();
    }
  };
  const read = (text: string, voice: VoiceConfig = {}) => {
    if (!supported) { setError('此浏览器不支持 Web Speech 语音朗读，请使用文字阅读。'); return; }
    stop(); setError(''); enabledRef.current = true; setEnabled(true);
    window.speechSynthesis.resume();
    queue.current.push({ text, voice }); pump();
  };
  const onEvent = useCallback((event: GameEvent) => {
    const item = eventSpeech(event);
    if (!enabledRef.current || !item) return;
    if (queue.current.length >= 100) { setError('朗读队列已满，部分新发言未朗读。可停止后重新开启。'); return; }
    queue.current.push(item); pumpRef.current();
  }, []);
  const changeRate = (value: number) => {
    rateRef.current = bounded(value, 1, 0.5, 2);
    setRate(rateRef.current);
  };
  return { supported, enabled, speaking, error, voiceCount, rate, changeRate, toggle, stop, read, onEvent };
}
export type SpeechControls = ReturnType<typeof useSpeech>;
