import { Text, View } from "react-native";

export function ListEmpty({
  title,
  detail,
}: {
  title: string;
  detail?: string | undefined;
}) {
  return (
    <View className="items-center justify-center px-6 py-16">
      <Text className="text-base font-medium text-foreground">{title}</Text>
      {detail ? (
        <Text className="mt-2 text-center text-sm text-muted-foreground">
          {detail}
        </Text>
      ) : null}
    </View>
  );
}

export function ListError({
  title = "Something went wrong",
  detail,
}: {
  title?: string | undefined;
  detail?: string | undefined;
}) {
  return (
    <View className="items-center justify-center px-6 py-16">
      <Text className="text-base font-medium text-destructive">{title}</Text>
      {detail ? (
        <Text className="mt-2 text-center text-sm text-muted-foreground">
          {detail}
        </Text>
      ) : null}
    </View>
  );
}
