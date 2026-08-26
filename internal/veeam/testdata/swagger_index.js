/* Source: https://gist.github.com/lamberta/3768814
 * Parse a string function definition and return a function object. Does not use eval.
 * @param {string} str
 * @return {function}
 *
 * Example:
 *  var f = function (x, y) { return x * y; };
 *  var g = parseFunction(f.toString());
 *  g(33, 3); //=> 99
 */
function parseFunction(str) {
    if (!str) return void (0);

    var fn_body_idx = str.indexOf('{'),
        fn_body = str.substring(fn_body_idx + 1, str.lastIndexOf('}')),
        fn_declare = str.substring(0, fn_body_idx),
        fn_params = fn_declare.substring(fn_declare.indexOf('(') + 1, fn_declare.lastIndexOf(')')),
        args = fn_params.split(',');

    args.push(fn_body);

    function Fn() {
        return Function.apply(this, args);
    }
    Fn.prototype = Function.prototype;

    return new Fn();
}

window.onload = function () {
    var configObject = JSON.parse('{"urls":[{"url":"/swagger/v1.3-rev2/swagger.json","name":"V1.3-REV2"},{"url":"/swagger/v1.3-rev1/swagger.json","name":"V1.3-REV1"},{"url":"/swagger/v1.3-rev0/swagger.json","name":"V1.3-REV0"},{"url":"/swagger/v1.2-rev1/swagger.json","name":"V1.2-REV1"},{"url":"/swagger/v1.2-rev0/swagger.json","name":"V1.2-REV0"},{"url":"/swagger/v1.1-rev2/swagger.json","name":"V1.1-REV2"},{"url":"/swagger/v1.1-rev1/swagger.json","name":"V1.1-REV1"},{"url":"/swagger/v1.1-rev0/swagger.json","name":"V1.1-REV0"}],"deepLinking":true,"persistAuthorization":false,"displayOperationId":false,"defaultModelsExpandDepth":1,"defaultModelExpandDepth":1,"defaultModelRendering":"example","displayRequestDuration":false,"docExpansion":"none","showExtensions":false,"showCommonExtensions":false,"supportedSubmitMethods":["get","put","post","delete","options","head","patch","trace"],"tryItOutEnabled":false}');
    var oauthConfigObject = JSON.parse('{"scopeSeparator":" ","scopes":[],"useBasicAuthenticationWithAccessCodeGrant":false,"usePkceWithAuthorizationCodeGrant":false}');

    // Workaround for https://github.com/swagger-api/swagger-ui/issues/5945
    configObject.urls.forEach(function (item) {
        if (item.url.startsWith("http") || item.url.startsWith("/")) return;
        item.url = window.location.href.replace("index.html", item.url).split('#')[0];
    });

    // If validatorUrl is not explicitly provided, disable the feature by setting to null
    if (!configObject.hasOwnProperty("validatorUrl"))
        configObject.validatorUrl = null;

    // If oauth2RedirectUrl isn't specified, use the built-in default
    if (!configObject.hasOwnProperty("oauth2RedirectUrl"))
        configObject.oauth2RedirectUrl = (new URL("oauth2-redirect.html", window.location.href)).href;

    // Apply mandatory parameters
    configObject.dom_id = "#swagger-ui";
    configObject.presets = [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset];
    configObject.layout = "StandaloneLayout";

    // Parse and add interceptor functions
    var interceptors = JSON.parse('{}');
    if (interceptors.RequestInterceptorFunction)
        configObject.requestInterceptor = parseFunction(interceptors.RequestInterceptorFunction);
    if (interceptors.ResponseInterceptorFunction)
        configObject.responseInterceptor = parseFunction(interceptors.ResponseInterceptorFunction);

    if (configObject.plugins) {
        configObject.plugins = configObject.plugins.map(eval);
    }
    
    // Begin Swagger UI call region

    const ui = SwaggerUIBundle(configObject);

    ui.initOAuth(oauthConfigObject);

    // End Swagger UI call region

    window.ui = ui;
}
