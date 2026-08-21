package app.pod.di

import android.content.Context
import app.pod.BuildConfig
import app.pod.api.Copywriter
import app.pod.api.OnboardingRepository
import app.pod.api.PodApiClient
import app.pod.api.PurchaseBroker
import app.pod.api.SessionRepository
import app.pod.api.SlotsRepository
import app.pod.api.TokenStore
import app.pod.api.UnavailablePurchaseBroker
import app.pod.api.WeekRepositories
import app.pod.api.generated.PurchaseSource
import app.pod.data.EncryptedTokenStore
import app.pod.data.NoPushTokenSource
import app.pod.data.PushRegistrar
import app.pod.data.PushTokenSource
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides
    @Singleton
    fun tokenStore(@ApplicationContext context: Context): TokenStore = EncryptedTokenStore(context)

    @Provides
    @Singleton
    fun apiClient(tokens: TokenStore): PodApiClient =
        PodApiClient(baseUrl = BuildConfig.API_BASE_URL, tokens = tokens)

    @Provides
    @Singleton
    fun copywriter(): Copywriter = Copywriter.bundled

    /**
     * One repository for the whole process: the sign-in state machine must not fork when
     * the activity is recreated.
     */
    @Provides
    @Singleton
    fun sessionRepository(client: PodApiClient): SessionRepository =
        SessionRepository(client, deviceName = android.os.Build.MODEL)

    /**
     * One repository for the whole process: a configuration change must not discard a
     * half-answered intent or restart the flow.
     */
    @Provides
    @Singleton
    fun onboardingRepository(client: PodApiClient): OnboardingRepository =
        OnboardingRepository(client)

    /**
     * No FCM configuration ships in this repository, so the default source never yields a
     * token and the app behaves as it does for anyone who declines push. Swap this
     * binding per environment to wire Firebase in.
     */
    @Provides
    @Singleton
    fun pushTokenSource(): PushTokenSource = NoPushTokenSource()

    @Provides
    @Singleton
    fun pushRegistrar(client: PodApiClient, tokens: PushTokenSource): PushRegistrar =
        PushRegistrar(client, tokens)

    /**
     * No Play Billing configuration ships in this repository, so the default broker
     * behaves like someone who cancelled — the product works without a paywall rather
     * than breaking around one. Swap this binding per environment to wire billing in,
     * the same seam the server has in selectReceiptVerifier.
     */
    @Provides
    @Singleton
    fun purchaseBroker(): PurchaseBroker = UnavailablePurchaseBroker()

    @Provides
    @Singleton
    fun slotsRepository(client: PodApiClient, broker: PurchaseBroker): SlotsRepository =
        SlotsRepository(client, broker, PurchaseSource.PLAY_STORE)

    /** One week repository per pod, kept for the process. */
    @Provides
    @Singleton
    fun weekRepositories(client: PodApiClient): WeekRepositories = WeekRepositories(client)
}
