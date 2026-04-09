/**
 * Custom Stack header that always pads its top by the system status bar
 * inset. Used as the `header` prop on the clusters and alerts Stack
 * navigators.
 *
 * Why a custom header instead of relying on `@react-navigation/native-stack`'s
 * default header?
 *
 * Under Android edge-to-edge mode (default in Expo SDK 53 + Android 15 /
 * API 35), the activity is drawn full-screen with no system bar
 * reservation. The native-stack header *should* automatically add status
 * bar padding via `react-native-safe-area-context`, but in nested-navigator
 * setups (Tabs containing a Stack with `headerShown: false` on the Tab
 * screen) the inset propagation can drop and the header ends up at y=0,
 * overlapping the status bar / camera punch.
 *
 * This component reads the inset directly via `useSafeAreaInsets()` and
 * applies it as `paddingTop`, so it works regardless of whether the
 * native-stack inset propagation is doing the right thing.
 *
 * Wired up via `header: (props) => <StackHeader {...props} />` on each
 * Stack's `screenOptions`. React Navigation passes `options`, `route`,
 * `navigation`, and `back` — we use `options.title`, `back` to know
 * whether to show the chevron, and `navigation.goBack()` for the tap.
 */

import { Text, TouchableOpacity, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { NativeStackHeaderProps } from "@react-navigation/native-stack";
import { ChevronLeft } from "lucide-react-native";

import { SearchButton } from "@/features/search/SearchButton";

export function StackHeader({ options, route, navigation, back }: NativeStackHeaderProps) {
  const insets = useSafeAreaInsets();
  const title =
    typeof options.title === "string" && options.title.length > 0
      ? options.title
      : route.name;

  return (
    <View
      style={{ paddingTop: insets.top }}
      className="border-b border-border bg-card"
    >
      <View className="h-12 flex-row items-center px-3">
        {back ? (
          <TouchableOpacity
            onPress={() => navigation.goBack()}
            className="-ml-1 p-1"
            hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
          >
            <ChevronLeft color="#fafafa" size={24} />
          </TouchableOpacity>
        ) : null}
        <Text
          className="ml-1 flex-1 text-base font-semibold text-foreground"
          numberOfLines={1}
        >
          {title}
        </Text>
        <SearchButton />
      </View>
    </View>
  );
}
