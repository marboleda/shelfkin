// The household switcher: the Pantry header below the wide-layout breakpoint,
// and a button in the side nav at and above it. Both open the same list (a
// bottom sheet, or a popover anchored below the side-nav button), which also
// offers the create and join forms.

import Feather from '@expo/vector-icons/Feather';
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode, type Ref } from 'react';
import { Modal, Pressable, ScrollView, Text, View, type ViewInstance } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { useCurrentHousehold, useHouseholds } from '@/household/current-household';
import { householdInitial, roleLabel, type Household } from '@/household/households';
import { useSwitchHousehold } from '@/household/navigation';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { focusElement, focusWaitsForModalDismiss } from './focus';
import { CreateHouseholdForm, JoinHouseholdForm } from './household-forms';

export const HOUSEHOLD_LIST_LABEL = 'Your households';

type Placement = 'header' | 'sideNav';
type Mode = 'closed' | 'list' | 'create' | 'join';
type Anchor = { x: number; y: number; width: number; height: number };

/** Width of the side-nav popover. It may be wider than the side nav, so long names have room. */
const POPOVER_WIDTH = 300;

export function switcherLabel(name: string): string {
  return `Switch household, current: ${name}`;
}

export function householdRowLabel(household: Household, current: boolean): string {
  return `${household.name}, ${roleLabel(household.role)}${current ? ', current' : ''}`;
}

export function HouseholdSwitcher({ placement }: { placement: Placement }) {
  const current = useCurrentHousehold();
  const [mode, setMode] = useState<Mode>('closed');
  const [anchor, setAnchor] = useState<Anchor | null>(null);
  const buttonRef = useRef<ViewInstance>(null);
  const restoreFocus = useRef(false);
  // The mode as of the last commit, for the Modal's `onDismiss`.
  const modeRef = useRef<Mode>(mode);
  useLayoutEffect(() => {
    modeRef.current = mode;
  }, [mode]);
  // The last form opened, so the dialog keeps its name while it closes.
  const [lastForm, setLastForm] = useState<'create' | 'join'>('create');
  const switchHousehold = useSwitchHousehold();

  const open = useCallback(() => {
    if (placement === 'sideNav') {
      buttonRef.current?.measureInWindow?.((x: number, y: number, width: number, height: number) => setAnchor({ x, y, width, height }));
    }
    setMode('list');
  }, [placement]);

  const close = useCallback(() => {
    restoreFocus.current = true;
    setMode('closed');
  }, []);

  // Back on the switcher button after the list or a form closes, however it
  // closed. On web this waits for the Modal's `onDismiss`, which comes after the
  // Modal has unmounted: while it is mounted, RN-Web's focus trap pulls focus
  // back inside it, and then focus falls to <body> when it unmounts.
  const returnFocus = useCallback(() => {
    if (modeRef.current === 'closed' && restoreFocus.current) {
      restoreFocus.current = false;
      focusElement(buttonRef.current);
    }
  }, []);
  useEffect(() => {
    if (mode === 'closed' && !focusWaitsForModalDismiss()) returnFocus();
  }, [mode, returnFocus]);

  const choose = useCallback(
    (householdId: string) => {
      switchHousehold(householdId);
      close();
    },
    [switchHousehold, close],
  );

  const list = (
    <HouseholdList
      currentId={current.id}
      onChoose={choose}
      onCreate={() => {
        setLastForm('create');
        setMode('create');
      }}
      onJoin={() => {
        setLastForm('join');
        setMode('join');
      }}
    />
  );
  const form =
    mode === 'create' ? (
      <CreateHouseholdForm autoFocus onCreated={close} />
    ) : mode === 'join' ? (
      <JoinHouseholdForm autoFocus onJoined={close} />
    ) : null;
  const formTitle = lastForm === 'create' ? 'Create a household' : 'Join with a code';

  return (
    <>
      <SwitcherButton
        ref={buttonRef}
        placement={placement}
        name={current.name}
        expanded={mode !== 'closed'}
        onPress={open}
      />
      {placement === 'header' ? (
        <BottomSheet
          visible={mode !== 'closed'}
          onClose={close}
          onDismiss={returnFocus}
          label={form ? formTitle : HOUSEHOLD_LIST_LABEL}
        >
          {form ? <FormPanel title={formTitle} onCancel={close} form={form} /> : list}
        </BottomSheet>
      ) : (
        <>
          <Popover visible={mode === 'list'} anchor={anchor} onClose={close} onDismiss={returnFocus}>
            {list}
          </Popover>
          <CenteredDialog visible={form !== null} onClose={close} onDismiss={returnFocus} label={formTitle}>
            {form ? <FormPanel title={formTitle} onCancel={close} form={form} /> : null}
          </CenteredDialog>
        </>
      )}
    </>
  );
}

type SwitcherButtonProps = {
  ref: Ref<ViewInstance>;
  placement: Placement;
  name: string;
  expanded: boolean;
  onPress: () => void;
};

/** The button that opens the list: the household name as the Pantry header, or a bordered side-nav button. */
function SwitcherButton({ ref, placement, name, expanded, onPress }: SwitcherButtonProps) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  const a11y = {
    role: 'button' as const,
    'aria-label': switcherLabel(name),
    'aria-expanded': expanded,
    accessibilityState: { expanded },
    // The label holds the full name, even when the text is truncated.
  };

  if (placement === 'header') {
    return (
      <Pressable ref={ref} testID="household-switcher-header" onPress={onPress} style={styles.headerButton} {...a11y}>
        <Text style={styles.headerName} numberOfLines={1} ellipsizeMode="tail">
          {name}
        </Text>
        <Feather name="chevron-down" size={24} color={colors.text} aria-hidden accessible={false} />
      </Pressable>
    );
  }

  return (
    <Pressable ref={ref} testID="household-switcher-side-nav" onPress={onPress} style={styles.sideButton} {...a11y}>
      <HouseholdTile name={name} size="small" />
      <Text style={styles.sideName} numberOfLines={1} ellipsizeMode="tail">
        {name}
      </Text>
      <View style={styles.upDown} aria-hidden accessible={false} importantForAccessibility="no-hide-descendants">
        <Feather name="chevron-up" size={12} color={colors.mutedText} />
        <Feather name="chevron-down" size={12} color={colors.mutedText} />
      </View>
    </Pressable>
  );
}

/** A square tile with the household's initial. */
export function HouseholdTile({ name, size }: { name: string; size: 'small' | 'large' }) {
  const styles = useThemedStyles(createStyles);
  return (
    <View
      testID="household-tile"
      style={[styles.tile, size === 'small' ? styles.tileSmall : styles.tileLarge]}
      aria-hidden
      accessible={false}
      importantForAccessibility="no-hide-descendants"
    >
      <Text style={[styles.tileText, size === 'small' ? styles.tileTextSmall : null]}>{householdInitial(name)}</Text>
    </View>
  );
}

type HouseholdListProps = {
  currentId: string;
  onChoose: (householdId: string) => void;
  onCreate: () => void;
  onJoin: () => void;
};

/** Every household, sorted by name, with the current one checked, then Create and Join. */
export function HouseholdList({ currentId, onChoose, onCreate, onJoin }: HouseholdListProps) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  const { state } = useHouseholds();
  const households = state.status === 'ready' ? state.households : [];

  return (
    <View style={styles.listBody}>
      <Text role="heading" aria-level={2} style={styles.listTitle}>
        {HOUSEHOLD_LIST_LABEL}
      </Text>
      <ScrollView style={styles.rowsScroll} contentContainerStyle={styles.rows}>
        {households.map((household, index) => {
          const isCurrent = household.id === currentId;
          return (
            <Pressable
              key={household.id}
              testID={`household-row-${household.id}`}
              // Focus starts on the current household's row when the list opens.
              ref={isCurrent ? focusElement : undefined}
              role="button"
              aria-label={householdRowLabel(household, isCurrent)}
              aria-current={isCurrent ? 'true' : undefined}
              accessibilityState={{ selected: isCurrent }}
              onPress={() => onChoose(household.id)}
              style={[styles.row, index > 0 && styles.rowDivider, isCurrent && styles.rowCurrent]}
            >
              <HouseholdTile name={household.name} size="large" />
              <View style={styles.rowText}>
                <Text style={styles.rowName}>{household.name}</Text>
                <Text style={styles.rowRole}>{roleLabel(household.role)}</Text>
              </View>
              {isCurrent ? (
                <Feather testID="household-current-check" name="check" size={22} color={colors.primary} aria-hidden accessible={false} />
              ) : null}
            </Pressable>
          );
        })}
      </ScrollView>
      <View style={styles.actions}>
        <ListAction label="Create a household" icon="plus" onPress={onCreate} />
        <ListAction label="Join with a code" icon="log-in" onPress={onJoin} />
      </View>
    </View>
  );
}

function ListAction({ label, icon, onPress }: { label: string; icon: 'plus' | 'log-in'; onPress: () => void }) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  return (
    <Pressable role="button" aria-label={label} onPress={onPress} style={styles.action}>
      <Feather name={icon} size={18} color={colors.text} aria-hidden accessible={false} />
      <Text style={styles.actionLabel}>{label}</Text>
    </Pressable>
  );
}

function FormPanel({ title, form, onCancel }: { title: string; form: ReactNode; onCancel: () => void }) {
  const styles = useThemedStyles(createStyles);
  return (
    <View style={styles.listBody}>
      <Text role="heading" aria-level={2} style={styles.listTitle}>
        {title}
      </Text>
      {form}
      <Pressable role="button" aria-label="Cancel" onPress={onCancel} style={styles.cancel}>
        <Text style={styles.cancelLabel}>Cancel</Text>
      </Pressable>
    </View>
  );
}

type OverlayProps = {
  visible: boolean;
  onClose: () => void;
  /** Called once the overlay has finished closing and is gone. */
  onDismiss: () => void;
  children: ReactNode;
};

/**
 * A full-window layer behind an overlay. Tapping or clicking it closes the
 * overlay. Not focusable or announced: screen reader users close with the
 * platform's escape gesture, the back button or Escape.
 *
 * A plain View with a touch responder, not a Pressable: on web a Pressable
 * gets a `tabindex` (-1 at best), and RN-Web's Modal focus trap moves focus to
 * the first element that accepts `focus()`, so it would land on the backdrop
 * when Tab wraps around or a dialog opens.
 */
function Backdrop({ onClose, dim }: { onClose: () => void; dim: boolean }) {
  const styles = useThemedStyles(createStyles);
  return (
    <View
      testID="overlay-backdrop"
      onStartShouldSetResponder={() => true}
      onResponderRelease={onClose}
      accessible={false}
      importantForAccessibility="no"
      style={[styles.backdrop, dim && styles.backdropDim]}
    />
  );
}

/** The phone list: a sheet from the bottom of the window. */
function BottomSheet({ visible, onClose, onDismiss, label, children }: OverlayProps & { label: string }) {
  const styles = useThemedStyles(createStyles);
  const insets = useSafeAreaInsets();
  return (
    <Modal
      visible={visible}
      transparent
      animationType="slide"
      onRequestClose={onClose}
      onDismiss={onDismiss}
      statusBarTranslucent
      navigationBarTranslucent
      aria-label={label}
    >
      <View style={styles.sheetLayer}>
        <Backdrop onClose={onClose} dim />
        <View
          testID="household-sheet"
          role="dialog"
          aria-modal
          aria-label={label}
          accessibilityViewIsModal
          onAccessibilityEscape={onClose}
          style={[styles.sheet, { paddingBottom: insets.bottom + 24 }]}
        >
          <View style={styles.grabber} />
          {children}
        </View>
      </View>
    </Modal>
  );
}

/** The wide-layout list: a popover directly below the side-nav button. */
function Popover({ visible, anchor, onClose, onDismiss, children }: OverlayProps & { anchor: Anchor | null }) {
  const styles = useThemedStyles(createStyles);
  const { spacing } = useTheme();
  const position = anchor
    ? { top: anchor.y + anchor.height + spacing.xs, left: anchor.x, width: Math.max(anchor.width, POPOVER_WIDTH) }
    : { top: spacing.xxl * 3, left: spacing.md, width: POPOVER_WIDTH };
  return (
    <Modal
      visible={visible}
      transparent
      animationType="none"
      onRequestClose={onClose}
      onDismiss={onDismiss}
      aria-label={HOUSEHOLD_LIST_LABEL}
    >
      <View style={styles.fill}>
        <Backdrop onClose={onClose} dim={false} />
        <View
          testID="household-popover"
          role="dialog"
          aria-modal
          aria-label={HOUSEHOLD_LIST_LABEL}
          accessibilityViewIsModal
          onAccessibilityEscape={onClose}
          style={[styles.popover, position]}
        >
          {children}
        </View>
      </View>
    </Modal>
  );
}

/** The wide-layout create and join forms: a dialog in the middle of the window. */
function CenteredDialog({ visible, onClose, onDismiss, label, children }: OverlayProps & { label: string }) {
  const styles = useThemedStyles(createStyles);
  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={onClose} onDismiss={onDismiss} aria-label={label}>
      <View style={styles.centeredLayer}>
        <Backdrop onClose={onClose} dim />
        <View
          testID="household-dialog"
          role="dialog"
          aria-modal
          aria-label={label}
          accessibilityViewIsModal
          onAccessibilityEscape={onClose}
          style={styles.dialog}
        >
          {children}
        </View>
      </View>
    </Modal>
  );
}

function createStyles(theme: Theme) {
  return {
    headerButton: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      alignSelf: 'flex-start' as const,
      maxWidth: '100%' as const,
      minHeight: MIN_TOUCH_TARGET,
      gap: theme.spacing.xs,
      borderRadius: theme.radii.sm,
    },
    headerName: {
      ...theme.typography.heading,
      color: theme.colors.text,
      flexShrink: 1,
    },
    sideButton: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm + 2,
      minHeight: 52,
      paddingVertical: theme.spacing.xs + 2,
      paddingHorizontal: theme.spacing.sm + 2,
      marginBottom: theme.spacing.sm + 2,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.md,
      backgroundColor: theme.colors.background,
    },
    sideName: {
      ...theme.typography.label,
      fontSize: 14,
      color: theme.colors.text,
      flex: 1,
      minWidth: 0,
    },
    upDown: {
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      flexShrink: 0,
    },
    tile: {
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      flexShrink: 0,
      backgroundColor: theme.colors.primary,
    },
    tileSmall: { width: 30, height: 30, borderRadius: theme.radii.sm },
    tileLarge: { width: 40, height: 40, borderRadius: theme.radii.md },
    tileText: { ...theme.typography.label, fontSize: 16, color: theme.colors.onPrimary },
    tileTextSmall: { fontSize: 14 },
    listBody: { gap: theme.spacing.lg, flexShrink: 1 },
    listTitle: { ...theme.typography.title, color: theme.colors.text, paddingHorizontal: theme.spacing.xs },
    rowsScroll: {
      flexGrow: 0,
      flexShrink: 1,
      backgroundColor: theme.colors.surface,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.card,
    },
    rows: {},
    row: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      minHeight: 64,
      paddingVertical: theme.spacing.sm + 2,
      paddingHorizontal: theme.spacing.md + 2,
    },
    rowDivider: { borderTopWidth: 1, borderTopColor: theme.colors.border },
    rowCurrent: { backgroundColor: theme.colors.primarySubtle },
    rowText: { flex: 1, minWidth: 0, gap: 2 },
    rowName: { ...theme.typography.label, fontSize: 16, color: theme.colors.text },
    rowRole: { ...theme.typography.caption, color: theme.colors.mutedText },
    actions: { flexDirection: 'row' as const, flexWrap: 'wrap' as const, gap: theme.spacing.sm },
    action: {
      flexGrow: 1,
      flexBasis: 120,
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.sm,
      minHeight: 52,
      paddingHorizontal: theme.spacing.md,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.card,
      backgroundColor: theme.colors.surface,
    },
    actionLabel: { ...theme.typography.label, color: theme.colors.text },
    cancel: {
      minHeight: MIN_TOUCH_TARGET,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
    },
    cancelLabel: { ...theme.typography.label, color: theme.colors.primary },
    fill: { flex: 1 },
    backdrop: {
      position: 'absolute' as const,
      top: 0,
      right: 0,
      bottom: 0,
      left: 0,
    },
    // The text colour, faded, keeps the scrim on-theme in light and dark mode.
    backdropDim: { backgroundColor: theme.colors.text, opacity: 0.45 },
    sheetLayer: { flex: 1, justifyContent: 'flex-end' as const },
    sheet: {
      maxHeight: '85%' as const,
      gap: theme.spacing.lg,
      paddingTop: theme.spacing.sm + 2,
      paddingHorizontal: theme.spacing.lg,
      backgroundColor: theme.colors.background,
      borderTopLeftRadius: theme.radii.lg + 4,
      borderTopRightRadius: theme.radii.lg + 4,
    },
    grabber: {
      alignSelf: 'center' as const,
      width: 40,
      height: 5,
      borderRadius: theme.radii.pill,
      backgroundColor: theme.colors.border,
    },
    popover: {
      position: 'absolute' as const,
      maxHeight: '80%' as const,
      padding: theme.spacing.md,
      backgroundColor: theme.colors.background,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.card,
      shadowColor: theme.colors.text,
      shadowOpacity: 0.15,
      shadowRadius: 16,
      shadowOffset: { width: 0, height: 4 },
      elevation: 8,
    },
    centeredLayer: {
      flex: 1,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      padding: theme.spacing.xl,
    },
    dialog: {
      width: '100%' as const,
      maxWidth: 440,
      padding: theme.spacing.xl,
      backgroundColor: theme.colors.background,
      borderRadius: theme.radii.lg,
    },
  };
}
