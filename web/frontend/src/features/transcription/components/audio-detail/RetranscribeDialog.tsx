import { useState, useEffect } from "react";
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
    DialogFooter,
} from "@/components/ui/dialog";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { Loader2, Languages, RotateCcw, X, AlertTriangle } from "lucide-react";
import { useRetranscribe } from "@/features/transcription/hooks/useAudioDetail";
import { useToast } from "@/components/ui/toast";

// Sentinel value for "let WhisperX auto-detect" — Radix Select forbids empty-string values.
const AUTO = "auto";

// Curated short list: Auto plus Romanian/English/Spanish/German/French and a few
// other common languages. Auto-detect stays the default.
const LANGUAGES: { value: string; label: string }[] = [
    { value: AUTO, label: "Auto-detect" },
    { value: "ro", label: "Romanian" },
    { value: "en", label: "English" },
    { value: "es", label: "Spanish" },
    { value: "de", label: "German" },
    { value: "fr", label: "French" },
    { value: "it", label: "Italian" },
    { value: "pt", label: "Portuguese" },
    { value: "nl", label: "Dutch" },
    { value: "ru", label: "Russian" },
    { value: "uk", label: "Ukrainian" },
    { value: "pl", label: "Polish" },
    { value: "tr", label: "Turkish" },
    { value: "ar", label: "Arabic" },
    { value: "zh", label: "Chinese" },
    { value: "ja", label: "Japanese" },
];

// NVIDIA Canary only supports these four, and it has no auto-detect: the
// backend maps `language` to `source_lang` and defaults it to "en" when unset,
// and CanaryAdapter rejects anything outside this list at processing time.
// Mirrors CANARY_LANGUAGES in TranscriptionConfigDialog.
const CANARY_LANGUAGES: { value: string; label: string }[] = [
    { value: "en", label: "English" },
    { value: "de", label: "German" },
    { value: "es", label: "Spanish" },
    { value: "fr", label: "French" },
];

interface RetranscribeDialogProps {
    audioId: string;
    isOpen: boolean;
    onClose: (open: boolean) => void;
    /** The language the job currently ran with, if any — used to preselect. */
    currentLanguage?: string;
    /** Engine the job runs on — decides which languages can be offered. */
    modelFamily?: string;
}

export function RetranscribeDialog({ audioId, isOpen, onClose, currentLanguage, modelFamily }: RetranscribeDialogProps) {
    const { toast } = useToast();
    const { mutate: retranscribe, isPending } = useRetranscribe(audioId);

    // Canary can neither auto-detect nor handle the full list, so offer only what
    // the engine actually accepts rather than letting the job fail mid-processing.
    const isCanary = modelFamily === "nvidia_canary";
    const languageOptions = isCanary ? CANARY_LANGUAGES : LANGUAGES;

    const defaultLanguage = (() => {
        if (!isCanary) return currentLanguage || AUTO;
        // Fall back to English if the stored language is one Canary cannot run.
        return CANARY_LANGUAGES.some((l) => l.value === currentLanguage) ? (currentLanguage as string) : "en";
    })();

    const [language, setLanguage] = useState<string>(defaultLanguage);

    // Preselect the job's current language whenever the dialog (re)opens.
    useEffect(() => {
        if (isOpen) {
            setLanguage(defaultLanguage);
        }
    }, [isOpen, defaultLanguage]);

    const handleRetranscribe = () => {
        // Canary always needs an explicit source language; never send null.
        const chosen = language === AUTO ? null : language;
        retranscribe(chosen, {
            onSuccess: () => {
                toast({
                    title: "Re-transcription started",
                    description:
                        chosen === null
                            ? "Re-running with automatic language detection."
                            : `Re-running in ${languageOptions.find((l) => l.value === chosen)?.label ?? chosen}.`,
                });
                onClose(false);
            },
            onError: (err) => {
                toast({
                    title: "Failed to start re-transcription",
                    description: err instanceof Error ? err.message : "Unknown error",
                });
            },
        });
    };

    return (
        <Dialog open={isOpen} onOpenChange={(open) => !isPending && onClose(open)}>
            <DialogContent className="sm:max-w-md w-[95vw] bg-[var(--bg-card)] border-[var(--border-subtle)] shadow-[var(--shadow-float)]">
                <DialogHeader className="border-b border-[var(--border-subtle)] pb-4">
                    <DialogTitle className="text-[var(--text-primary)] flex items-center gap-2 text-xl font-bold tracking-tight">
                        <Languages className="h-5 w-5 text-[var(--brand-solid)]" />
                        Re-transcribe
                    </DialogTitle>
                    <DialogDescription className="text-[var(--text-secondary)]">
                        Run transcription again — useful when auto-detect picked the wrong language.
                        All other settings (model, device, diarization) are kept.
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-4">
                    <div className="space-y-2">
                        <label className="block text-xs font-medium text-[var(--text-tertiary)] uppercase tracking-wider">
                            Language
                        </label>
                        <Select value={language} onValueChange={setLanguage} disabled={isPending}>
                            <SelectTrigger className="w-full">
                                <SelectValue placeholder={isCanary ? "English" : "Auto-detect"} />
                            </SelectTrigger>
                            <SelectContent className="max-h-[300px]">
                                {languageOptions.map((lang) => (
                                    <SelectItem key={lang.value} value={lang.value}>
                                        {lang.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <p className="text-xs text-[var(--text-tertiary)]">
                            {isCanary ? (
                                <>This recording runs on NVIDIA Canary, which needs an explicit source
                                language and only supports these four.</>
                            ) : (
                                <>Leave on <span className="font-medium">Auto-detect</span> unless the language
                                was detected incorrectly.</>
                            )}
                        </p>
                    </div>

                    <div className="flex items-start gap-2 rounded-[var(--radius-card)] border border-amber-500/30 bg-amber-500/10 p-3 text-xs text-[var(--text-secondary)]">
                        <AlertTriangle className="h-4 w-4 flex-shrink-0 text-amber-500 mt-0.5" />
                        <span>
                            This replaces the current transcript. Any existing AI summary describes the
                            old one, so it is hidden until you generate a new summary.
                        </span>
                    </div>
                </div>

                <DialogFooter className="gap-2">
                    <Button variant="outline" onClick={() => onClose(false)} disabled={isPending}>
                        <X className="h-4 w-4 mr-1" />
                        Cancel
                    </Button>
                    <Button onClick={handleRetranscribe} disabled={isPending} className="min-w-[140px]">
                        {isPending ? (
                            <>
                                <Loader2 className="h-4 w-4 mr-1 animate-spin" />
                                Starting…
                            </>
                        ) : (
                            <>
                                <RotateCcw className="h-4 w-4 mr-1" />
                                Re-transcribe
                            </>
                        )}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
