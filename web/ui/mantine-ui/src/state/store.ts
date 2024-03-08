import { configureStore } from "@reduxjs/toolkit";
import queryPageSlice from "./queryPageSlice";
import { prometheusApi } from "./api";
import settingsSlice from "./settingsSlice";

const store = configureStore({
  reducer: {
    settings: settingsSlice,
    queryPage: queryPageSlice,
    [prometheusApi.reducerPath]: prometheusApi.reducer,
  },
  middleware: (getDefaultMiddleware) =>
    getDefaultMiddleware().concat(prometheusApi.middleware),
});

// Infer the `RootState` and `AppDispatch` types from the store itself
export type RootState = ReturnType<typeof store.getState>;
// Inferred type: {posts: PostsState, comments: CommentsState, users: UsersState}
export type AppDispatch = typeof store.dispatch;

export default store;
